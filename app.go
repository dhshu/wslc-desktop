// This file is the Wails assembly layer described by docs/CONTRACT.md section C.
//
// It does exactly three things:
//
//  1. owns the *service.Service,
//  2. provides the production service.Emitter that publishes onto the Wails
//     event bus, and
//  3. re-exports every service method under the same name so the generated
//     frontend bindings are a 1:1 mirror of the business layer.
//
// The `context.Context` parameter of the service layer is deliberately NOT
// re-exported: Wails v2.16.0 does not inject a context into bound methods
// (internal/binding/reflect.go records every input parameter and
// internal/frontend/dispatcher/calls.go requires the JS call to supply exactly
// that many arguments), so a ctx parameter would both demand a bogus argument
// from the page and arrive as nil. Instead each forwarder passes the
// application context captured in OnStartup, which is cancelled on shutdown.
// The shapes below therefore match frontend/wailsjs/go/main/App.d.ts exactly.
package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/service"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

const (
	// EventName is the Wails event that carries every service.OutputEvent
	// (container logs, terminal I/O, build/pull progress, task transitions).
	// It aliases service.EventName so the two layers cannot drift apart.
	EventName = service.EventName

	// EnvEventName carries one service.EnvStatus snapshot so the "环境自检"
	// view can render without waiting for its own call.
	EnvEventName = service.EnvEventName
)

// wailsEmitter is the production service.Emitter.
//
// It is intentionally a separate, unexported type rather than a method on App:
// Wails binds *every* exported method of a bound struct, so an exported
// App.Emit would leak the broadcast primitive into the page and force Wails to
// generate TypeScript models for service.OutputEvent (including time.Time).
type wailsEmitter struct {
	app *App
}

var _ service.Emitter = (*wailsEmitter)(nil)

// Emit publishes one output event. Calls that arrive before startup or after
// shutdown are dropped: runtime.EventsEmit panics when the context does not
// carry Wails' event bus.
func (e *wailsEmitter) Emit(event service.OutputEvent) {
	if e == nil || e.app == nil {
		return
	}
	ctx := e.app.wailsContext()
	if ctx == nil {
		return
	}
	runtime.EventsEmit(ctx, EventName, event)
}

// App is the struct bound to the frontend.
type App struct {
	mu sync.RWMutex
	// ctx is the Wails application context, valid between OnStartup and
	// OnShutdown. It is used for both runtime.EventsEmit and service calls.
	ctx context.Context
	// envStatus caches the startup environment check so the event survives the
	// race between the check finishing and the page subscribing.
	envStatus *service.EnvStatus
	// envEmitted guards the "exactly one env:status event" contract.
	envEmitted bool

	svc *service.Service
}

// NewApp wires the business layer to the Wails shell.
func NewApp(runner wslc.Runner) *App {
	app := &App{}
	app.svc = service.NewService(runner, &wailsEmitter{app: app})
	return app
}

// ---------------------------------------------------------------------------
// lifecycle (unexported: never bound)
// ---------------------------------------------------------------------------

// startup stores the application context and kicks off the environment check.
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	go a.checkEnv()
}

// domReady emits the cached environment snapshot. OnStartup runs before the
// page exists, so an event sent there can never be observed; emitting on the
// first of {check finished, DOM ready} makes the push land exactly once and be
// actually received.
func (a *App) domReady(context.Context) {
	a.emitEnvStatus()
}

// shutdown releases the context so late service callbacks cannot reach Wails.
func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	a.ctx = nil
	a.mu.Unlock()
}

// checkEnv runs the startup environment probe and publishes the result.
func (a *App) checkEnv() {
	status, err := a.svc.EnvCheck(a.callContext())
	if err != nil {
		log.Printf("wslc Desktop: 环境自检失败: %v", err)
		// Keep the snapshot useful: a hard failure is still a problem the user
		// must see in the environment view.
		status.Problems = append(append([]string(nil), status.Problems...), err.Error())
	}
	a.mu.Lock()
	a.envStatus = &status
	a.mu.Unlock()
	a.emitEnvStatus()
}

// emitEnvStatus sends EnvEventName at most once, and only when both a
// completed check and the Wails context are available.
func (a *App) emitEnvStatus() {
	a.mu.Lock()
	if a.envEmitted || a.envStatus == nil || a.ctx == nil {
		a.mu.Unlock()
		return
	}
	a.envEmitted = true
	status := *a.envStatus
	ctx := a.ctx
	a.mu.Unlock()

	runtime.EventsEmit(ctx, EnvEventName, status)
}

// wailsContext returns the Wails context, or nil outside the app lifetime.
func (a *App) wailsContext() context.Context {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ctx
}

// callContext is the context handed to the service layer. It is the Wails
// context (so quitting cancels in-flight commands) or, before startup, a
// background context so direct service calls still work in tests.
func (a *App) callContext() context.Context {
	if ctx := a.wailsContext(); ctx != nil {
		return ctx
	}
	return context.Background()
}

// ---------------------------------------------------------------------------
// 环境
// ---------------------------------------------------------------------------

// EnvCheck reports whether wslc and the container service are usable.
func (a *App) EnvCheck() (service.EnvStatus, error) {
	return a.svc.EnvCheck(a.callContext())
}

// ---------------------------------------------------------------------------
// 容器
// ---------------------------------------------------------------------------

// ListContainers lists containers, applying the client-side filter.
func (a *App) ListContainers(f service.ContainerFilter) ([]domain.Container, error) {
	return a.svc.ListContainers(a.callContext(), f)
}

// StartContainer starts a stopped container.
func (a *App) StartContainer(ref string) (string, error) {
	return a.svc.StartContainer(a.callContext(), ref)
}

// StopContainer stops a running container after an optional grace period.
func (a *App) StopContainer(ref string, timeoutSec int) (string, error) {
	return a.svc.StopContainer(a.callContext(), ref, timeoutSec)
}

// RestartContainer restarts a container.
func (a *App) RestartContainer(ref string, timeoutSec int) (string, error) {
	return a.svc.RestartContainer(a.callContext(), ref, timeoutSec)
}

// KillContainer sends a signal to a container.
func (a *App) KillContainer(ref string, signal string) (string, error) {
	return a.svc.KillContainer(a.callContext(), ref, signal)
}

// RemoveContainer deletes a container, optionally forcing and removing volumes.
func (a *App) RemoveContainer(ref string, force bool, volumes bool) (string, error) {
	return a.svc.RemoveContainer(a.callContext(), ref, force, volumes)
}

// RunContainer creates and starts a container from options.
func (a *App) RunContainer(opts service.RunContainerOptions) (string, error) {
	return a.svc.RunContainer(a.callContext(), opts)
}

// InspectContainer returns the raw wslc inspect JSON for a container.
func (a *App) InspectContainer(ref string) (json.RawMessage, error) {
	return a.svc.InspectContainer(a.callContext(), ref)
}

// ContainerStats returns resource usage samples.
func (a *App) ContainerStats(all bool) ([]domain.ContainerStats, error) {
	return a.svc.ContainerStats(a.callContext(), all)
}

// ---------------------------------------------------------------------------
// 日志与终端（流式）
// ---------------------------------------------------------------------------

// StartLogs begins streaming container logs and returns the stream id.
func (a *App) StartLogs(ref string, opts service.LogsOptions) (string, error) {
	return a.svc.StartLogs(a.callContext(), ref, opts)
}

// StopStream ends a log or terminal stream.
func (a *App) StopStream(streamID string) error {
	return a.svc.StopStream(a.callContext(), streamID)
}

// StartTerminal opens an interactive exec session and returns the stream id.
func (a *App) StartTerminal(ref string, opts service.ExecOptions, cols int, rows int) (string, error) {
	return a.svc.StartTerminal(a.callContext(), ref, opts, cols, rows)
}

// TerminalWrite forwards keystrokes to an interactive session.
func (a *App) TerminalWrite(streamID string, data string) error {
	return a.svc.TerminalWrite(a.callContext(), streamID, data)
}

// TerminalResize reports a new terminal geometry.
func (a *App) TerminalResize(streamID string, cols int, rows int) error {
	return a.svc.TerminalResize(a.callContext(), streamID, cols, rows)
}

// ---------------------------------------------------------------------------
// 镜像
// ---------------------------------------------------------------------------

// ListImages lists images.
func (a *App) ListImages(all bool) ([]domain.Image, error) {
	return a.svc.ListImages(a.callContext(), all)
}

// PullImage starts an asynchronous pull and returns the task id.
func (a *App) PullImage(ref string) (string, error) {
	return a.svc.PullImage(a.callContext(), ref)
}

// BuildImage starts an asynchronous build and returns the task id.
func (a *App) BuildImage(opts service.BuildOptions) (string, error) {
	return a.svc.BuildImage(a.callContext(), opts)
}

// RemoveImage deletes an image.
func (a *App) RemoveImage(ref string, force bool) (string, error) {
	return a.svc.RemoveImage(a.callContext(), ref, force)
}

// TagImage tags an image.
func (a *App) TagImage(source string, target string) (string, error) {
	return a.svc.TagImage(a.callContext(), source, target)
}

// InspectImage returns the raw wslc inspect JSON for an image.
func (a *App) InspectImage(ref string) (json.RawMessage, error) {
	return a.svc.InspectImage(a.callContext(), ref)
}

// ---------------------------------------------------------------------------
// 卷与网络
// ---------------------------------------------------------------------------

// ListVolumes lists volumes.
func (a *App) ListVolumes() ([]domain.Volume, error) {
	return a.svc.ListVolumes(a.callContext())
}

// CreateVolume creates a named volume.
func (a *App) CreateVolume(name string, driver string) (string, error) {
	return a.svc.CreateVolume(a.callContext(), name, driver)
}

// RemoveVolume deletes a volume.
func (a *App) RemoveVolume(name string, force bool) (string, error) {
	return a.svc.RemoveVolume(a.callContext(), name, force)
}

// ListNetworks lists networks.
func (a *App) ListNetworks() ([]domain.Network, error) {
	return a.svc.ListNetworks(a.callContext())
}

// CreateNetwork creates a network.
func (a *App) CreateNetwork(name string, driver string, subnet string, gateway string, internal bool) (string, error) {
	return a.svc.CreateNetwork(a.callContext(), name, driver, subnet, gateway, internal)
}

// RemoveNetwork deletes a network.
func (a *App) RemoveNetwork(name string, force bool) (string, error) {
	return a.svc.RemoveNetwork(a.callContext(), name, force)
}

// ---------------------------------------------------------------------------
// 系统
// ---------------------------------------------------------------------------

// PruneContainers removes stopped containers.
func (a *App) PruneContainers() (service.PruneResult, error) {
	return a.svc.PruneContainers(a.callContext())
}

// PruneImages removes unused images.
func (a *App) PruneImages(all bool) (service.PruneResult, error) {
	return a.svc.PruneImages(a.callContext(), all)
}

// ListTasks lists background tasks (pull, build, streams).
func (a *App) ListTasks() ([]service.Task, error) {
	return a.svc.ListTasks(a.callContext())
}

// CancelTask cancels a running task.
func (a *App) CancelTask(id string) error {
	return a.svc.CancelTask(a.callContext(), id)
}

// StreamEvents starts the `wslc events` stream and returns the stream id.
func (a *App) StreamEvents() (string, error) {
	return a.svc.StreamEvents(a.callContext())
}

// ---------------------------------------------------------------------------
// 设置（代理 / 镜像源）
// ---------------------------------------------------------------------------

// LoadSettings returns the persisted app settings (proxy, registry mirror).
func (a *App) LoadSettings() (service.AppSettings, error) {
	return a.svc.LoadSettings(a.callContext())
}

// SaveSettings validates and persists the given app settings.
func (a *App) SaveSettings(in service.AppSettings) (service.AppSettings, error) {
	return a.svc.SaveSettings(a.callContext(), in)
}

// TestMirror probes one registry endpoint with a tiny public image.
func (a *App) TestMirror(endpoint string) (service.MirrorProbe, error) {
	return a.svc.TestMirror(a.callContext(), endpoint)
}

// TestProxy probes whether the given proxy URL is reachable from the host.
func (a *App) TestProxy(url string) (service.ProxyProbe, error) {
	return a.svc.TestProxy(a.callContext(), url)
}
