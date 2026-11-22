// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package fakecluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"time"

	"github.com/globlit/k9s/internal/client"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
)

// Handle requests before routing; return true to skip the default handler.
type Handler func(w http.ResponseWriter, r *http.Request) bool

// Serve the dynamic store over HTTP through the existing RBAC and failure hooks.
// Point the kubeconfig and REST config at this server.
func WithAPIServer() Option {
	return func(c *Cluster) { c.serve = true }
}

// Run the handler before routing each request.
func (c *Cluster) Intercept(h Handler) {
	c.recMx.Lock()
	defer c.recMx.Unlock()
	c.hooks = append(c.hooks, h)
}

// Return recorded requests as "METHOD /path".
func (c *Cluster) RESTCalls() []string {
	c.recMx.Lock()
	defer c.recMx.Unlock()

	return append([]string(nil), c.rest...)
}

func (c *Cluster) startServer() {
	srv := httptest.NewServer(http.HandlerFunc(c.serveHTTP))
	c.t.Cleanup(srv.Close)
	c.srvURL = srv.URL
}

func (c *Cluster) serveHTTP(w http.ResponseWriter, r *http.Request) {
	c.recMx.Lock()
	c.rest = append(c.rest, r.Method+" "+r.URL.Path)
	hooks := append([]Handler(nil), c.hooks...)
	c.recMx.Unlock()
	for _, h := range hooks {
		if h(w, r) {
			return
		}
	}

	switch r.URL.Path {
	case "/version":
		writeJSON(w, http.StatusOK, c.disc.FakedServerVersion)
	case "/api":
		writeJSON(w, http.StatusOK, &metav1.APIVersions{
			TypeMeta: metav1.TypeMeta{Kind: "APIVersions"},
			Versions: []string{"v1"},
		})
	case "/apis":
		writeJSON(w, http.StatusOK, c.groupList())
	default:
		c.serveResource(w, r)
	}
}

func (c *Cluster) groupList() *metav1.APIGroupList {
	l := metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"}}
	seen := make(map[string]int)
	for _, rl := range c.disc.Resources {
		gv, err := schema.ParseGroupVersion(rl.GroupVersion)
		if err != nil || gv.Group == "" {
			continue
		}
		v := metav1.GroupVersionForDiscovery{GroupVersion: rl.GroupVersion, Version: gv.Version}
		i, ok := seen[gv.Group]
		if !ok {
			seen[gv.Group] = len(l.Groups)
			l.Groups = append(l.Groups, metav1.APIGroup{Name: gv.Group, PreferredVersion: v})
			i = len(l.Groups) - 1
		}
		l.Groups[i].Versions = append(l.Groups[i].Versions, v)
	}

	return &l
}

type request struct {
	res           Resource
	ns, name, sub string
}

func (c *Cluster) parse(p string) (schema.GroupVersion, []string, bool) {
	ss := strings.Split(strings.Trim(p, "/"), "/")
	switch {
	case len(ss) >= 2 && ss[0] == "api":
		return schema.GroupVersion{Version: ss[1]}, ss[2:], true
	case len(ss) >= 3 && ss[0] == "apis":
		return schema.GroupVersion{Group: ss[1], Version: ss[2]}, ss[3:], true
	default:
		return schema.GroupVersion{}, nil, false
	}
}

func (c *Cluster) serveResource(w http.ResponseWriter, r *http.Request) {
	gv, rest, ok := c.parse(r.URL.Path)
	if !ok {
		writeErr(w, apierrors.NewNotFound(schema.GroupResource{}, r.URL.Path))
		return
	}
	if len(rest) == 0 {
		for _, l := range c.disc.Resources {
			if l.GroupVersion == gv.String() {
				cp := *l
				cp.Kind, cp.APIVersion = "APIResourceList", "v1"
				writeJSON(w, http.StatusOK, &cp)
				return
			}
		}
		writeErr(w, apierrors.NewNotFound(schema.GroupResource{}, r.URL.Path))
		return
	}
	var req request
	if len(rest) >= 3 && rest[0] == "namespaces" {
		req.ns, rest = rest[1], rest[2:]
	}
	if len(rest) > 3 {
		writeErr(w, apierrors.NewNotFound(schema.GroupResource{}, r.URL.Path))
		return
	}
	gvr := gv.WithResource(rest[0])
	if len(rest) > 1 {
		req.name = rest[1]
	}
	if len(rest) > 2 {
		req.sub = rest[2]
	}
	for _, res := range c.resources {
		if res.gvr() == gvr {
			req.res = res
			break
		}
	}
	if req.res.GVR == nil || (req.sub != "" && req.sub != "scale") {
		writeErr(w, apierrors.NewNotFound(gvr.GroupResource(), req.name))
		return
	}

	o, err := c.do(r, &req)
	if err != nil {
		writeErr(w, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "as=Table") && req.sub == "" && r.Method == http.MethodGet {
		o = c.table(o, &req, r.URL.Query().Get("includeObject"))
	}
	writeJSON(w, http.StatusOK, o)
}

func (c *Cluster) do(r *http.Request, req *request) (runtime.Object, error) {
	ctx := r.Context()
	gvr := req.res.gvr()
	dr := c.dyn.Resource(gvr).Namespace(req.ns)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}

	switch {
	case req.sub == "scale" && r.Method == http.MethodGet:
		return c.invoke(k8stesting.NewGetSubresourceAction(gvr, req.ns, "scale", req.name))
	case req.sub == "scale" && r.Method == http.MethodPut:
		var s autoscalingv1.Scale
		if err := decode(r, body, &s); err != nil {
			return nil, apierrors.NewBadRequest(err.Error())
		}
		return c.invoke(k8stesting.NewUpdateSubresourceAction(gvr, "scale", req.ns, &s))
	case r.Method == http.MethodGet && r.URL.Query().Get("watch") == "true":
		return nil, apierrors.NewMethodNotSupported(gvr.GroupResource(), "watch")
	case r.Method == http.MethodGet && req.name == "":
		return dr.List(ctx, metav1.ListOptions{LabelSelector: r.URL.Query().Get("labelSelector")})
	case r.Method == http.MethodGet:
		return dr.Get(ctx, req.name, metav1.GetOptions{})
	case r.Method == http.MethodDelete && req.name != "":
		var opts metav1.DeleteOptions
		if len(body) > 0 {
			if err := decode(r, body, &opts); err != nil {
				return nil, apierrors.NewBadRequest(err.Error())
			}
		}
		if err := dr.Delete(ctx, req.name, opts); err != nil {
			return nil, err
		}
		return &metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: metav1.StatusSuccess}, nil
	case r.Method == http.MethodPatch && req.name != "":
		pt := types.PatchType(strings.Split(r.Header.Get("Content-Type"), ";")[0])
		return dr.Patch(ctx, req.name, pt, body, metav1.PatchOptions{})
	case r.Method == http.MethodPost && req.name == "", r.Method == http.MethodPut && req.name != "":
		u, err := decodeUnstructured(r, body)
		if err != nil {
			return nil, apierrors.NewBadRequest(err.Error())
		}
		if r.Method == http.MethodPost {
			return dr.Create(ctx, u, metav1.CreateOptions{})
		}
		return dr.Update(ctx, u, metav1.UpdateOptions{})
	default:
		return nil, apierrors.NewMethodNotSupported(gvr.GroupResource(), r.Method)
	}
}

func isProto(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Content-Type"), "protobuf")
}

func decode(r *http.Request, body []byte, o runtime.Object) error {
	if isProto(r) {
		_, _, err := scheme.Codecs.UniversalDeserializer().Decode(body, nil, o)
		return err
	}

	return json.Unmarshal(body, o)
}

func decodeUnstructured(r *http.Request, body []byte) (*unstructured.Unstructured, error) {
	var u unstructured.Unstructured
	if !isProto(r) {
		return &u, u.UnmarshalJSON(body)
	}
	o, gvk, err := scheme.Codecs.UniversalDeserializer().Decode(body, nil, nil)
	if err != nil {
		return nil, err
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(o)
	if err != nil {
		return nil, err
	}
	u.Object = m
	u.SetGroupVersionKind(*gvk)

	return &u, nil
}

func (c *Cluster) invoke(a k8stesting.Action) (runtime.Object, error) {
	o, err := c.dyn.Invokes(a, nil)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, apierrors.NewNotFound(a.GetResource().GroupResource(), "")
	}

	return o, nil
}

func (c *Cluster) scaleReaction(a k8stesting.Action) (bool, runtime.Object, error) {
	if a.GetSubresource() != "scale" {
		return false, nil, nil
	}
	gvr, ns := a.GetResource(), a.GetNamespace()
	var name string
	ua, isUpdate := a.(k8stesting.UpdateAction)
	if isUpdate {
		s, ok := ua.GetObject().(*autoscalingv1.Scale)
		if !ok {
			return true, nil, apierrors.NewBadRequest("not a scale")
		}
		name = s.Name
	} else if ga, ok := a.(k8stesting.GetAction); ok {
		name = ga.GetName()
	}
	u, err := c.DynGet(client.NewGVR(path.Join(gvr.Group, gvr.Version, gvr.Resource)), ns, name)
	if err != nil {
		return true, nil, err
	}
	if isUpdate {
		s, _ := ua.GetObject().(*autoscalingv1.Scale)
		if err := unstructured.SetNestedField(u.Object, int64(s.Spec.Replicas), "spec", "replicas"); err != nil {
			return true, nil, err
		}
		if err := c.dyn.Tracker().Update(gvr, u, ns); err != nil {
			return true, nil, err
		}
	}
	replicas := nestedInt(u, "spec", "replicas")

	return true, &autoscalingv1.Scale{
		TypeMeta:   metav1.TypeMeta{Kind: "Scale", APIVersion: "autoscaling/v1"},
		ObjectMeta: metav1.ObjectMeta{Name: u.GetName(), Namespace: u.GetNamespace(), ResourceVersion: u.GetResourceVersion()},
		Spec:       autoscalingv1.ScaleSpec{Replicas: int32(replicas)},
		Status:     autoscalingv1.ScaleStatus{Replicas: int32(nestedInt(u, "status", "replicas"))},
	}, nil
}

type column struct {
	name, typ string
	cell      func(*unstructured.Unstructured) any
}

func (c *Cluster) table(o runtime.Object, req *request, include string) *metav1.Table {
	var items []unstructured.Unstructured
	t := metav1.Table{TypeMeta: metav1.TypeMeta{Kind: "Table", APIVersion: "meta.k8s.io/v1"}}
	switch v := o.(type) {
	case *unstructured.UnstructuredList:
		items = v.Items
		t.ResourceVersion = v.GetResourceVersion()
	case *unstructured.Unstructured:
		items = []unstructured.Unstructured{*v}
	}
	cols := columnsFor(req.res)
	for _, col := range cols {
		t.ColumnDefinitions = append(t.ColumnDefinitions, metav1.TableColumnDefinition{Name: col.name, Type: col.typ})
	}
	for i := range items {
		u := &items[i]
		row := metav1.TableRow{}
		for _, col := range cols {
			row.Cells = append(row.Cells, col.cell(u))
		}
		switch include {
		case "None":
		case "Object":
			row.Object.Raw, _ = u.MarshalJSON()
		default:
			row.Object.Raw, _ = json.Marshal(map[string]any{
				"kind": "PartialObjectMetadata", "apiVersion": "meta.k8s.io/v1", "metadata": u.Object["metadata"],
			})
		}
		t.Rows = append(t.Rows, row)
	}

	return &t
}

func columnsFor(r Resource) []column {
	name := column{"Name", "string", func(u *unstructured.Unstructured) any { return u.GetName() }}
	ageCol := column{"Age", "string", ageOf}
	str := func(n string, fields ...string) column {
		return column{n, "string", func(u *unstructured.Unstructured) any {
			s, _, _ := unstructured.NestedString(u.Object, fields...)
			return s
		}}
	}
	num := func(n string, fields ...string) column {
		return column{n, "integer", func(u *unstructured.Unstructured) any { return nestedInt(u, fields...) }}
	}
	ready := column{"Ready", "string", func(u *unstructured.Unstructured) any {
		return fmt.Sprintf("%d/%d", nestedInt(u, "status", "readyReplicas"), nestedInt(u, "spec", "replicas"))
	}}

	switch r.gvr().Resource {
	case "pods":
		return []column{name, {"Ready", "string", podReady}, str("Status", "status", "phase"), ageCol}
	case "deployments", "statefulsets":
		return []column{name, ready, ageCol}
	case "replicasets":
		return []column{name, num("Desired", "spec", "replicas"), num("Current", "status", "replicas"),
			num("Ready", "status", "readyReplicas"), ageCol}
	case "daemonsets":
		return []column{name, num("Desired", "status", "desiredNumberScheduled"),
			num("Current", "status", "currentNumberScheduled"), num("Ready", "status", "numberReady"), ageCol}
	case "events":
		return []column{{"Last Seen", "string", ageOf}, str("Type", "type"), str("Reason", "reason"),
			{"Object", "string", eventObject}, {"Message", "string", eventMessage}}
	case "priorityclasses":
		return []column{name, num("Value", "value"), str("Description", "description"), ageCol}
	default:
		return []column{name, ageCol}
	}
}

func podReady(u *unstructured.Unstructured) any {
	cc, _, _ := unstructured.NestedSlice(u.Object, "spec", "containers")
	ss, _, _ := unstructured.NestedSlice(u.Object, "status", "containerStatuses")
	var n int
	for _, s := range ss {
		if m, ok := s.(map[string]any); ok && m["ready"] == true {
			n++
		}
	}

	return fmt.Sprintf("%d/%d", n, len(cc))
}

func eventObject(u *unstructured.Unstructured) any {
	for _, f := range []string{"regarding", "involvedObject"} {
		if m, ok, _ := unstructured.NestedStringMap(u.Object, f); ok {
			return strings.ToLower(m["kind"]) + "/" + m["name"]
		}
	}

	return ""
}

func eventMessage(u *unstructured.Unstructured) any {
	for _, f := range []string{"note", "message"} {
		if s, ok, _ := unstructured.NestedString(u.Object, f); ok {
			return s
		}
	}

	return ""
}

func ageOf(u *unstructured.Unstructured) any {
	ts := u.GetCreationTimestamp()
	if ts.IsZero() {
		return "<unknown>"
	}

	return duration.HumanDuration(time.Since(ts.Time))
}

func nestedInt(u *unstructured.Unstructured, fields ...string) int64 {
	v, ok, _ := unstructured.NestedFieldNoCopy(u.Object, fields...)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func writeJSON(w http.ResponseWriter, code int, o any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(o)
}

func writeErr(w http.ResponseWriter, err error) {
	var se apierrors.APIStatus
	st := apierrors.NewInternalError(err).ErrStatus
	if errors.As(err, &se) {
		st = se.Status()
	}
	if st.Code == 0 {
		st.Code = http.StatusInternalServerError
	}
	st.Kind, st.APIVersion = "Status", "v1"
	writeJSON(w, int(st.Code), &st)
}
