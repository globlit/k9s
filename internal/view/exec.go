// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of K9s

package view

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/globlit/k9s/internal/client"
	"github.com/globlit/k9s/internal/config"
	"github.com/globlit/k9s/internal/model"
	"github.com/globlit/k9s/internal/render"
	"github.com/globlit/k9s/internal/slogs"
	"github.com/globlit/k9s/internal/ui"
	"github.com/globlit/k9s/internal/ui/dialog"
	"github.com/google/shlex"
	v1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	shellCheck    = `command -v bash >/dev/null && exec bash || exec sh`
	winShellCheck = `where powershell >nul 2>&1 && powershell || cmd`
	bannerFmt     = "<<K9s-Shell>> Pod: %s | Container: %s \n"
	outputPrefix  = "[output]"
	failedPrefix  = "[failed]"
)

var editorEnvVars = []string{"K9S_EDITOR", "KUBE_EDITOR", "EDITOR"}

type shellOpts struct {
	clear, background bool
	pipes             []string
	binary            string
	banner            string
	args              []string
}

func (s shellOpts) String() string {
	return fmt.Sprintf("%s %s", s.binary, strings.Join(s.args, " "))
}

func runK(a *App, opts *shellOpts) error {
	bin, err := exec.LookPath("kubectl")
	if errors.Is(err, exec.ErrDot) {
		return fmt.Errorf("kubectl command must not be in the current working directory: %w", err)
	}
	if err != nil {
		return fmt.Errorf("kubectl command is not in your path: %w", err)
	}
	args := []string{opts.args[0]}
	if u, err := a.Conn().Config().ImpersonateUser(); err == nil {
		args = append(args, "--as", u)
	}
	args = append(args, impersonateGroupArgs(a)...)
	if isInsecure := a.Conn().Config().Flags().Insecure; isInsecure != nil && *isInsecure {
		args = append(args, "--insecure-skip-tls-verify")
	}
	args = append(args, "--context", a.Config.K9s.ActiveContextName())
	if cfg := a.Conn().Config().Flags().KubeConfig; cfg != nil && *cfg != "" {
		args = append(args, "--kubeconfig", *cfg)
	}
	if len(args) > 0 {
		opts.args = append(args, opts.args[1:]...)
	}
	opts.binary = bin

	suspended, errChan, stChan := run(a, opts)
	if !suspended {
		return fmt.Errorf("unable to run command")
	}
	var errs error
	for v := range stChan {
		if msg, ok := strings.CutPrefix(v, failedPrefix); ok {
			errs = errors.Join(errs, errors.New(strings.TrimSpace(msg)))
			continue
		}
		slog.Debug("stdout", slogs.Line, v)
	}
	for e := range errChan {
		errs = errors.Join(errs, e)
	}

	return errs
}

func run(a *App, opts *shellOpts) (ok bool, errC chan error, outC chan string) {
	errChan := make(chan error, 1)
	statusChan := make(chan string, 1)

	if opts.background {
		if err := execute(opts, statusChan); err != nil {
			errChan <- err
			a.Flash().Errf("Exec failed %q: %s", opts, err)
		}
		close(errChan)
		return true, errChan, statusChan
	}

	a.Halt()
	defer a.Resume()

	ran := false
	ok = a.Suspend(func() {
		ran = true
		// Child owns the terminal: pass stderr through.
		ui.SetStderrCaptured(false)
		defer ui.SetStderrCaptured(true)
		if err := execute(opts, statusChan); err != nil {
			errChan <- err
			a.Flash().Errf("Exec failed %q: %s", opts, err)
		}
		close(errChan)
	})
	if !ran {
		// Command never ran: close channels to unblock callers.
		close(errChan)
		close(statusChan)
	}

	return ok, errChan, statusChan
}

func edit(a *App, opts *shellOpts) bool {
	var (
		bin string
		err error
	)
	for _, e := range editorEnvVars {
		env := os.Getenv(e)
		if env == "" {
			continue
		}

		// There may be situations where the user sets the editor as the binary
		// followed by some arguments (e.g. "code -w" to make it work with vscode)
		//
		// In such cases, the actual binary is only the first token
		envTokens, shlexErr := shlex.Split(env)
		if shlexErr != nil || len(envTokens) == 0 {
			continue
		}

		if bin, err = exec.LookPath(envTokens[0]); err == nil {
			// Make sure the path is at the end (this allows running editors
			// with custom options)
			if len(envTokens) > 1 {
				originalArgs := opts.args
				opts.args = envTokens[1:]
				opts.args = append(opts.args, originalArgs...)
			}

			break
		}
	}
	if bin == "" {
		a.Flash().Errf("You must set at least one of those env vars: %s", strings.Join(editorEnvVars, "|"))
		return false
	}
	opts.binary, opts.background = bin, false

	suspended, errChan, _ := run(a, opts)
	if !suspended {
		a.Flash().Errf("edit command failed")
	}
	status := true
	for e := range errChan {
		a.Flash().Err(e)
		status = false
	}

	return status
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t\n\"'\\") {
		return s
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

func execute(opts *shellOpts, statusChan chan<- string) error {
	if opts.clear {
		clearScreen()
	}
	ctx, cancel := context.WithCancel(context.Background())
	// Background commands release the context on completion (see pipe).
	defer func() {
		if !opts.background {
			cancel()
			clearScreen()
		}
	}()

	var interrupted atomic.Bool
	if !opts.background {
		// Forward Ctrl-C to foreground commands only. Always unregister to
		// restore default signal handling.
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigChan)
		go func() {
			select {
			case sig := <-sigChan:
				slog.Debug("Command canceled with signal", slogs.Sig, sig)
				interrupted.Store(true)
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	cmds := make([]*exec.Cmd, 0, 1)
	cmd := exec.CommandContext(ctx, opts.binary, opts.args...)
	slog.Debug("Exec command", slogs.Command, opts)

	if env := os.Getenv("K9S_EDITOR"); env != "" {
		// There may be situations where the user sets the editor as the binary
		// followed by some arguments (e.g. "code -w" to make it work with vscode)
		//
		// In such cases, the actual binary is only the first token
		if binTokens, err := shlex.Split(env); err == nil && len(binTokens) > 0 {
			if bin, err := exec.LookPath(binTokens[0]); err == nil {
				binTokens[0] = bin
				for i := range binTokens {
					binTokens[i] = shellQuote(binTokens[i])
				}
				cmd.Env = append(os.Environ(), fmt.Sprintf("KUBE_EDITOR=%s", strings.Join(binTokens, " ")))
			}
		}
	}

	cmds = append(cmds, cmd)

	for _, p := range opts.pipes {
		tokens, err := shlex.Split(p)
		if err != nil || len(tokens) == 0 {
			slog.Warn("Skipping invalid pipe command", slogs.Command, p, slogs.Error, err)
			continue
		}
		cmd := exec.CommandContext(ctx, tokens[0], tokens[1:]...)
		slog.Debug("Exec command", slogs.Command, cmd)
		cmds = append(cmds, cmd)
	}

	var o, e bytes.Buffer
	err := pipe(ctx, cancel, opts, statusChan, &o, &e, cmds...)
	if err != nil && !interrupted.Load() {
		slog.Error("Pipe Exec failed",
			slogs.Error, err,
			slogs.Command, cmds,
		)
		return errors.Join(err, fmt.Errorf("%s", e.String()))
	}

	return nil
}

func runKu(ctx context.Context, a *App, opts *shellOpts) (string, error) {
	bin, err := exec.LookPath("kubectl")
	if errors.Is(err, exec.ErrDot) {
		slog.Error("Kubectl exec can not reside in current working directory", slogs.Error, err)
		return "", err
	}
	if err != nil {
		slog.Error("Kubectl exec not found", slogs.Error, err)
		return "", err
	}
	var args []string
	if u, err := a.Conn().Config().ImpersonateUser(); err == nil {
		args = append(args, "--as", u)
	}
	args = append(args, impersonateGroupArgs(a)...)
	args = append(args, "--context", a.Config.K9s.ActiveContextName())
	if cfg := a.Conn().Config().Flags().KubeConfig; cfg != nil && *cfg != "" {
		args = append(args, "--kubeconfig", *cfg)
	}
	if len(args) > 0 {
		opts.args = append(args, opts.args...)
	}
	opts.binary, opts.background = bin, false

	return oneShoot(ctx, opts)
}

func oneShoot(ctx context.Context, opts *shellOpts) (string, error) {
	if opts.clear {
		clearScreen()
	}

	slog.Debug("Executing command",
		slogs.Bin, opts.binary,
		slogs.Args, strings.Join(opts.args, " "),
	)
	cmd := exec.CommandContext(ctx, opts.binary, opts.args...)

	var err error
	buff := bytes.NewBufferString("")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, buff, buff
	if !opts.clear {
		// TUI owns the terminal: keep stdin from the child.
		cmd.Stdin = nil
	}
	_, _ = cmd.Stdout.Write([]byte(opts.banner))
	err = cmd.Run()

	return strings.Trim(buff.String(), "\n"), err
}

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

const (
	k9sShell           = "k9s-shell"
	k9sShellRetryCount = 50
	k9sShellRetryDelay = 2 * time.Second
)

func launchNodeShell(v model.Igniter, a *App, node string) {
	if err := nukeK9sShell(a); err != nil {
		a.Flash().Errf("Cleaning node shell failed: %s", err)
		return
	}

	msg := fmt.Sprintf("Launching node shell on %s...", node)
	d := a.Styles.Dialog()
	dialog.ShowPrompt(&d, a.Content.Pages, a.QueueUpdateDraw, "Launching", msg, func(ctx context.Context) {
		err := launchShellPod(ctx, a, node)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				a.Flash().Errf("Launching node shell failed: %s", err)
			}
			return
		}

		go launchPodShell(v, a)
	}, func() {
		if err := nukeK9sShell(a); err != nil {
			a.Flash().Errf("Cleaning node shell failed: %s", err)
			return
		}
	})
}

func launchPodShell(v model.Igniter, a *App) {
	if a.Config.K9s.ShellPod == nil {
		slog.Error("Shell pod not configured!")
		return
	}

	defer func() {
		if err := nukeK9sShell(a); err != nil {
			a.Flash().Errf("Launching node shell failed: %s", err)
			return
		}
	}()

	v.Stop()
	defer v.Start()

	ns := a.Config.K9s.ShellPod.Namespace
	if err := sshIn(a, client.FQN(ns, k9sShellPodName()), k9sShell); err != nil {
		a.Flash().Errf("Launching node shell failed: %s", err)
	}
}

func sshIn(a *App, fqn, co string) error {
	cfg := a.Config.K9s.ShellPod
	platform, err := getPodOS(a.factory, fqn)
	if err != nil {
		slog.Warn("os detect failed", slogs.Error, err)
	}

	args := buildShellArgs("exec", fqn, co, a.Conn().Config().Flags())
	args = append(args, "--")
	if len(cfg.Command) > 0 {
		args = append(args, cfg.Command...)
		args = append(args, cfg.Args...)
	} else if platform == windowsOS {
		args = append(args, "cmd", "/c", winShellCheck)
	} else {
		args = append(args, "sh", "-c", shellCheck)
	}
	slog.Debug("Running command with args", slogs.Args, args)

	c := color.New(color.BgGreen).Add(color.FgBlack).Add(color.Bold)
	err = runK(a, &shellOpts{
		clear:  true,
		banner: c.Sprintf(bannerFmt, fqn, co),
		args:   args},
	)
	if err != nil {
		return fmt.Errorf("shell exec failed: %w", err)
	}

	return nil
}

func nukeK9sShell(a *App) error {
	ct, err := a.Config.K9s.ActiveContext()
	if err != nil {
		return err
	}
	if !ct.FeatureGates.NodeShell || a.Config.K9s.ShellPod == nil {
		return nil
	}

	ns := a.Config.K9s.ShellPod.Namespace
	// Allow time on slow links: never leak a privileged node shell pod.
	ctx, cancel := context.WithTimeout(context.Background(), nodeShellDeleteTimeout)
	defer cancel()

	dial, err := a.Conn().Dial()
	if err != nil {
		return err
	}

	err = dial.CoreV1().Pods(ns).Delete(ctx, k9sShellPodName(), metav1.DeleteOptions{})
	if kerrors.IsNotFound(err) {
		return nil
	}

	return err
}

const nodeShellDeleteTimeout = 10 * time.Second

func launchShellPod(ctx context.Context, a *App, node string) (err error) {
	spo := a.Config.K9s.ShellPod
	spec, err := k9sShellPod(node, spo)
	if err != nil {
		return err
	}

	dial, err := a.Conn().Dial()
	if err != nil {
		return err
	}

	conn := dial.CoreV1().Pods(spo.Namespace)
	if _, err = conn.Create(ctx, spec, metav1.CreateOptions{}); err != nil {
		return err
	}
	// Delete the privileged pod when the shell fails to start.
	defer func() {
		if err != nil {
			if e := nukeK9sShell(a); e != nil {
				slog.Error("Node shell pod cleanup failed", slogs.Error, e)
			}
		}
	}()

	for i := range k9sShellRetryCount {
		o, err := a.factory.Get(client.PodGVR, client.FQN(spo.Namespace, k9sShellPodName()), true, labels.Everything())
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(k9sShellRetryDelay):
				continue
			}
		}

		var pod v1.Pod
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(o.(*unstructured.Unstructured).Object, &pod); err != nil {
			return err
		}
		slog.Debug("Checking k9s shell pod retries",
			slogs.Retry, i,
			slogs.PodPhase, pod.Status.Phase,
		)
		if pod.Status.Phase == v1.PodRunning {
			return nil
		}
		if reason, failed := shellPodFailed(&pod); failed {
			return fmt.Errorf("shell pod on node %s failed to start: %s", node, reason)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(k9sShellRetryDelay):
		}
	}

	return fmt.Errorf("unable to launch shell pod on node %s", node)
}

// shellPodFailed reports pod states that never reach Running, to fail fast.
func shellPodFailed(pod *v1.Pod) (string, bool) {
	switch pod.Status.Phase {
	case v1.PodFailed, v1.PodSucceeded:
		return string(pod.Status.Phase), true
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case "ImagePullBackOff", "InvalidImageName", "CreateContainerConfigError":
				return w.Reason + ": " + w.Message, true
			}
		}
	}

	return "", false
}

func k9sShellPodName() string {
	return fmt.Sprintf("%s-%d", k9sShell, os.Getpid())
}

func k9sShellPod(node string, cfg *config.ShellPod) (*v1.Pod, error) {
	var grace int64
	var priv = true

	res, err := asResource(cfg.Limits)
	if err != nil {
		return nil, err
	}

	slog.Debug("Shell pod config", slogs.ShellPodCfg, cfg)
	c := v1.Container{
		Name:            k9sShell,
		Image:           cfg.Image,
		ImagePullPolicy: cfg.ImagePullPolicy,
		VolumeMounts: []v1.VolumeMount{
			{
				Name:      "root-vol",
				MountPath: "/host",
				ReadOnly:  true,
			},
		},
		Resources: res,
		Stdin:     true,
		TTY:       cfg.TTY,
		SecurityContext: &v1.SecurityContext{
			Privileged: &priv,
		},
	}
	v := []v1.Volume{
		{
			Name: "root-vol",
			VolumeSource: v1.VolumeSource{
				HostPath: &v1.HostPathVolumeSource{
					Path: "/",
				},
			},
		},
	}
	if len(cfg.Command) != 0 {
		c.Command = cfg.Command
	}
	if len(cfg.Args) > 0 {
		c.Args = cfg.Args
	}
	if len(cfg.HostPathVolume) > 0 {
		for _, h := range cfg.HostPathVolume {
			c.VolumeMounts = append(c.VolumeMounts, v1.VolumeMount{
				Name:      h.Name,
				MountPath: h.MountPath,
				ReadOnly:  h.ReadOnly,
			})
			v = append(v, v1.Volume{
				Name: h.Name,
				VolumeSource: v1.VolumeSource{
					HostPath: &v1.HostPathVolumeSource{
						Path: h.HostPath,
					},
				},
			})
		}
	}
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k9sShellPodName(),
			Namespace: cfg.Namespace,
			Labels:    cfg.Labels,
		},
		Spec: v1.PodSpec{
			NodeName:                      node,
			RestartPolicy:                 v1.RestartPolicyNever,
			HostPID:                       true,
			HostNetwork:                   true,
			ImagePullSecrets:              cfg.ImagePullSecrets,
			TerminationGracePeriodSeconds: &grace,
			Volumes:                       v,
			Containers:                    []v1.Container{c},
			Tolerations: []v1.Toleration{
				{
					Operator: v1.TolerationOperator("Exists"),
				},
			},
		},
	}, nil
}

func asResource(r config.Limits) (v1.ResourceRequirements, error) {
	ll := make(v1.ResourceList, len(r))
	for _, n := range []v1.ResourceName{v1.ResourceCPU, v1.ResourceMemory} {
		if r[n] == "" {
			return v1.ResourceRequirements{}, fmt.Errorf("missing shell pod %s limit", n)
		}
		q, err := resource.ParseQuantity(r[n])
		if err != nil {
			return v1.ResourceRequirements{}, fmt.Errorf("invalid shell pod %s limit %q: %w", n, r[n], err)
		}
		ll[n] = q
	}

	return v1.ResourceRequirements{Limits: ll}, nil
}

// pipe runs cmds, chaining stdout to stdin. Always close statusChan on
// completion. Call done when background commands terminate.
func pipe(_ context.Context, done context.CancelFunc, opts *shellOpts, statusChan chan<- string, w, e *bytes.Buffer, cmds ...*exec.Cmd) error {
	if len(cmds) == 0 {
		close(statusChan)
		return nil
	}

	if len(cmds) == 1 {
		cmd := cmds[0]
		if opts.background {
			// Keep background commands off the tview terminal.
			cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, w, e
			go func() {
				defer done()
				defer close(statusChan)
				if err := cmd.Run(); err != nil {
					slog.Error("Command exec failed",
						slogs.Command, cmd.String(),
						slogs.Error, err,
						"stderr", e.String(),
					)
					statusChan <- fmt.Sprintf("%s %s", failedPrefix, failureReason(err, e))
					return
				}
				for _, l := range strings.Split(w.String(), "\n") {
					if l != "" {
						statusChan <- fmt.Sprintf("%s %s", outputPrefix, l)
					}
				}
				statusChan <- fmt.Sprintf("Command completed successfully: %q", render.Truncate(cmd.String(), 20))
				slog.Info("Command ran successfully", slogs.Command, cmd.String())
			}()
			return nil
		}
		defer close(statusChan)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, ui.RealStderr()
		_, _ = cmd.Stdout.Write([]byte(opts.banner))

		slog.Debug("Exec started")
		err := cmd.Run()
		var ex *exec.ExitError
		// Check if exec failed from a signal
		if errors.As(err, &ex) && !ex.Exited() {
			return nil
		}
		slog.Debug("Command exec done", slogs.Error, err)
		if err == nil {
			statusChan <- fmt.Sprintf("Command completed successfully: %q", cmd.String())
		}
		if err != nil {
			err = fmt.Errorf("command failed. Check k9s logs: %w", err)
		}

		return err
	}

	defer close(statusChan)
	if opts.background {
		defer done()
	}
	last := len(cmds) - 1
	cmds[0].Stdin = os.Stdin
	for i := range cmds {
		cmds[i].Stderr = ui.RealStderr()
		if i < last {
			// Use an OS pipe: the next command gets EOF when cmds[i] exits.
			out, err := cmds[i].StdoutPipe()
			if err != nil {
				return err
			}
			cmds[i+1].Stdin = out
		}
	}
	cmds[last].Stdout = os.Stdout

	started := make([]*exec.Cmd, 0, len(cmds))
	var errs error
	for _, cmd := range cmds {
		slog.Debug("Starting command", slogs.Command, cmd)
		if err := cmd.Start(); err != nil {
			errs = err
			break
		}
		started = append(started, cmd)
	}
	if errs != nil {
		// Downstream start failed: kill earlier commands blocked on full pipes.
		for _, cmd := range started {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}
	// Reap started commands: no zombies.
	for i := len(started) - 1; i >= 0; i-- {
		if err := started[i].Wait(); err != nil {
			var ex *exec.ExitError
			if errors.As(err, &ex) && !ex.Exited() {
				continue
			}
			errs = errors.Join(errs, err)
		}
	}

	return errs
}

// failureReason returns the last stderr line, or the exec error.
func failureReason(err error, stderr *bytes.Buffer) string {
	ll := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if l := strings.TrimSpace(ll[len(ll)-1]); l != "" {
		return l
	}

	return err.Error()
}

// impersonateGroupArgs passes one --as-group flag per group: kubectl reads
// "a,b" as a single group.
func impersonateGroupArgs(a *App) []string {
	gg := a.Conn().Config().Flags().ImpersonateGroup
	if gg == nil {
		return nil
	}
	args := make([]string, 0, 2*len(*gg))
	for _, g := range *gg {
		if g != "" {
			args = append(args, "--as-group", g)
		}
	}

	return args
}
