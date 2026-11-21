package dao

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/derailed/k9s/internal"
	"github.com/derailed/k9s/internal/client"
	"github.com/derailed/k9s/internal/slogs"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/cache"
)

var MxRecorder *Recorder

const (
	seriesCacheSize   = 600
	seriesCacheExpiry = 3 * time.Hour
	seriesRecordRate  = 1 * time.Minute
	nodeMetrics       = "node"
	podMetrics        = "pod"
)

type MetricsChan chan TimeSeries

type TimeSeries []Point

type Point struct {
	Time  time.Time
	Tags  map[string]string
	Value client.NodeMetrics
}

type Recorder struct {
	conn   client.Connection
	series *cache.LRUExpireCache
	sink   *mxSink
	mx     sync.RWMutex
}

// mxSink delivers metrics to a single watcher. Each Watch gets its own sink
// so a stale watch can never close or block a newer watcher's channel.
type mxSink struct {
	ch     MetricsChan
	mx     sync.Mutex
	closed bool
}

func newMxSink() *mxSink {
	return &mxSink{ch: make(MetricsChan, 2)}
}

// send delivers ts without blocking: if the consumer is slow or gone the
// point is dropped rather than stalling the producer (and the UI).
func (s *mxSink) send(ts TimeSeries) {
	if s == nil {
		return
	}
	s.mx.Lock()
	defer s.mx.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- ts:
	default:
		slog.Debug("Dropping metrics point: consumer is not keeping up")
	}
}

func (s *mxSink) close() {
	if s == nil {
		return
	}
	s.mx.Lock()
	defer s.mx.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

func DialRecorder(c client.Connection) *Recorder {
	if MxRecorder != nil {
		return MxRecorder
	}
	MxRecorder = &Recorder{
		conn:   c,
		series: cache.NewLRUExpireCache(seriesCacheSize),
	}

	return MxRecorder
}

func ResetRecorder(c client.Connection) {
	MxRecorder = nil
	DialRecorder(c)
}

func (r *Recorder) Clear() {
	r.mx.Lock()
	defer r.mx.Unlock()

	kk := r.series.Keys()
	for _, k := range kk {
		r.series.Remove(k)
	}
}

func (r *Recorder) dispatchSeries(sink *mxSink, kind, ns string) {
	if sink == nil {
		return
	}
	kk := r.series.Keys()
	hour := time.Now().Add(-1 * time.Hour)
	ts := make(TimeSeries, 0, len(kk))
	for _, k := range kk {
		if v, ok := r.series.Get(k); ok {
			if pt, cool := v.(Point); cool {
				if pt.Tags["type"] != kind || pt.Time.Sub(hour) < 0 {
					continue
				}
				switch kind {
				case nodeMetrics:
					ts = append(ts, pt)
				case podMetrics:
					if client.IsAllNamespaces(ns) || pt.Tags["namespace"] == ns {
						ts = append(ts, pt)
					}
				}
			}
		}
	}
	if len(ts) > 0 {
		sink.send(ts)
	}
}

func (r *Recorder) Watch(ctx context.Context, ns string) MetricsChan {
	sink := newMxSink()
	r.mx.Lock()
	prev := r.sink
	r.sink = sink
	r.mx.Unlock()
	prev.close()

	go func() {
		defer internal.LogPanic("metrics recorder")
		kind := podMetrics
		if client.IsAllNamespaces(ns) {
			kind = nodeMetrics
		}
		switch kind {
		case podMetrics:
			if err := r.recordPodMetrics(ctx, sink, ns); err != nil {
				slog.Error("Record pod metrics failed", slogs.Error, err)
			}
		case nodeMetrics:
			if err := r.recordNodeMetrics(ctx, sink); err != nil {
				slog.Error("Record node metrics failed", slogs.Error, err)
			}
		}
		r.dispatchSeries(sink, kind, ns)
		<-ctx.Done()
		sink.close()
		r.mx.Lock()
		if r.sink == sink {
			r.sink = nil
		}
		r.mx.Unlock()
	}()

	return sink.ch
}

func (r *Recorder) Record(ctx context.Context) error {
	if err := r.recordNodeMetrics(ctx, nil); err != nil {
		return err
	}
	return r.recordPodMetrics(ctx, nil, client.NamespaceAll)
}

func (r *Recorder) recordNodeMetrics(ctx context.Context, sink *mxSink) error {
	f, ok := ctx.Value(internal.KeyFactory).(Factory)
	if !ok {
		return errors.New("expecting factory in context")
	}
	nn, err := FetchNodes(ctx, f, "")
	if err != nil {
		return err
	}

	go func() {
		defer internal.LogPanic("node metrics recorder")
		r.recordClusterMetrics(ctx, sink, nn)
		t := time.NewTicker(seriesRecordRate)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.recordClusterMetrics(ctx, sink, nn)
			}
		}
	}()

	return nil
}

func (r *Recorder) recordClusterMetrics(ctx context.Context, sink *mxSink, nn *v1.NodeList) {
	dial := client.DialMetrics(r.conn)
	nmx, err := dial.FetchNodesMetrics(ctx)
	if err != nil {
		slog.Error("Fetch node metrics failed", slogs.Error, err)
		return
	}

	mx := make(client.NodesMetrics, len(nn.Items))
	dial.NodesMetrics(nn, nmx, mx)
	var cmx client.NodeMetrics
	for _, m := range mx {
		cmx.CurrentCPU += m.CurrentCPU
		cmx.CurrentMEM += m.CurrentMEM
		cmx.AllocatableCPU += m.AllocatableCPU
		cmx.AllocatableMEM += m.AllocatableMEM
		cmx.TotalCPU += m.TotalCPU
		cmx.TotalMEM += m.TotalMEM
	}
	pt := Point{
		Time:  time.Now(),
		Value: cmx,
		Tags: map[string]string{
			"type": nodeMetrics,
		},
	}
	if len(nn.Items) > 0 {
		r.series.Add(pt.Time, pt, seriesCacheExpiry)
	}
	sink.send(TimeSeries{pt})
}

func (r *Recorder) recordPodMetrics(ctx context.Context, sink *mxSink, ns string) error {
	go func() {
		defer internal.LogPanic("pod metrics recorder")
		if err := r.recordPodsMetrics(ctx, sink, ns); err != nil {
			slog.Error("Record pod metrics failed", slogs.Error, err)
		}
		t := time.NewTicker(seriesRecordRate)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := r.recordPodsMetrics(ctx, sink, ns); err != nil {
					slog.Error("Record pod metrics failed", slogs.Error, err)
				}
			}
		}
	}()

	return nil
}

func (r *Recorder) recordPodsMetrics(ctx context.Context, sink *mxSink, ns string) error {
	f, ok := ctx.Value(internal.KeyFactory).(Factory)
	if !ok {
		return errors.New("expecting factory in context")
	}
	pp, err := FetchPods(ctx, f, ns)
	if err != nil {
		return err
	}

	pt := Point{
		Time:  time.Now(),
		Value: client.NodeMetrics{},
		Tags: map[string]string{
			"namespace": ns,
			"type":      podMetrics,
		},
	}
	dial := client.DialMetrics(r.conn)
	for i := range pp.Items {
		p := pp.Items[i]
		fqn := client.FQN(p.Namespace, p.Name)
		pmx, err := dial.FetchPodMetrics(ctx, fqn)
		if err != nil {
			continue
		}
		for _, c := range pmx.Containers {
			pt.Value.CurrentCPU += c.Usage.Cpu().MilliValue()
			pt.Value.CurrentMEM += client.ToMB(c.Usage.Memory().Value())
		}
	}
	if len(pp.Items) > 0 {
		pt.Value.AllocatableCPU = pt.Value.CurrentCPU
		pt.Value.AllocatableMEM = pt.Value.CurrentMEM
		r.series.Add(pt.Time, pt, seriesCacheExpiry)
		sink.send(TimeSeries{pt})
	}

	return nil
}

// FetchPods retrieves all pods in a given namespace.
func FetchPods(_ context.Context, f Factory, ns string) (*v1.PodList, error) {
	auth, err := f.Client().CanI(ns, client.PodGVR, "pods", []string{client.ListVerb})
	if err != nil {
		return nil, err
	}
	if !auth {
		return nil, fmt.Errorf("user is not authorized to list pods")
	}

	oo, err := f.List(client.PodGVR, ns, false, labels.Everything())
	if err != nil {
		return nil, err
	}
	pp := make([]v1.Pod, 0, len(oo))
	for _, o := range oo {
		var pod v1.Pod
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(o.(*unstructured.Unstructured).Object, &pod)
		if err != nil {
			return nil, err
		}
		pp = append(pp, pod)
	}

	return &v1.PodList{Items: pp}, nil
}
