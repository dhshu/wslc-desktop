package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// ListImages lists images; all includes intermediate layers.
func (s *Service) ListImages(ctx context.Context, all bool) ([]domain.Image, error) {
	args := []string{"image", "list", "--format", "json"}
	if all {
		args = append(args, "--all")
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdImageList, Args: args})
	if err != nil {
		return nil, err
	}
	return wslc.ParseImages(res.Stdout)
}

// PullImage starts an asynchronous pull and returns its task id. Progress is
// broadcast on ChannelPull.
//
// When registry mirroring is enabled the reference is rewritten before it is
// sent to wslc, because wslc has no registry-mirror configuration of its own
// (see microsoft/WSL#40951). "alpine:3.20" becomes
// "docker.m.daocloud.io/library/alpine:3.20" and the rewritten value is the
// one recorded on the task, so the UI shows exactly what was pulled.
func (s *Service) PullImage(ctx context.Context, ref string) (string, error) {
	target, err := requireRef("镜像引用", ref)
	if err != nil {
		return "", err
	}
	target = s.rewriteImageRef(target)
	if err := contextError(ctx); err != nil {
		return "", err
	}
	streamCtx, cancel := context.WithCancel(normalizeContext(ctx))
	sess, task := s.startStream(TaskKindPull, ChannelPull, target, "", cancel)
	// Timeout must be a negative time.Duration (0 is *time.Duration(0), which
	// the runner treats as "use the default 60s"). -1 time.Nanosecond is the
	// canonical "unlimited" value used for every streaming command.
	spec := wslc.Spec{Kind: wslc.CmdImagePull, Args: []string{"image", "pull", target}, Timeout: -1 * time.Nanosecond}
	go s.pumpCommand(streamCtx, sess, spec)
	return task.ID, nil
}

// BuildImage starts an asynchronous build and returns its task id. Progress is
// broadcast on ChannelBuild.
//
// Argument order is frozen: image build [--file F] [--tag T…] [--build-arg K=V…]
// [--target T] [--no-cache] [--pull] [--label K=V…] [--progress P] CONTEXT.
// An empty context means the current directory.
func (s *Service) BuildImage(ctx context.Context, opts BuildOptions) (string, error) {
	contextPath := strings.TrimSpace(opts.Context)
	if contextPath == "" {
		contextPath = "."
	}
	if strings.HasPrefix(contextPath, "-") || strings.ContainsAny(contextPath, "\x00\r\n") {
		return "", errors.New("service: 构建上下文非法")
	}

	args := []string{"image", "build"}
	if dockerfile := strings.TrimSpace(opts.Dockerfile); dockerfile != "" {
		value, err := requireText("Dockerfile 路径", dockerfile)
		if err != nil {
			return "", err
		}
		args = append(args, "--file", value)
	}
	for _, tag := range opts.Tags {
		value, err := requireRef("镜像标签", strings.TrimSpace(tag))
		if err != nil {
			return "", err
		}
		args = append(args, "--tag", value)
	}
	for _, buildArg := range opts.BuildArgs {
		value, err := validateKeyValue("build-arg", buildArg)
		if err != nil {
			return "", err
		}
		args = append(args, "--build-arg", value)
	}
	if target := strings.TrimSpace(opts.Target); target != "" {
		value, err := requireText("构建目标", target)
		if err != nil {
			return "", err
		}
		args = append(args, "--target", value)
	}
	if opts.NoCache {
		args = append(args, "--no-cache")
	}
	if opts.Pull {
		args = append(args, "--pull")
	}
	for _, label := range opts.Labels {
		value, err := validateKeyValue("标签", label)
		if err != nil {
			return "", err
		}
		args = append(args, "--label", value)
	}
	progress, err := validateProgress(opts.Progress)
	if err != nil {
		return "", err
	}
	if progress != "" {
		args = append(args, "--progress", progress)
	}
	args = append(args, contextPath)

	if err := contextError(ctx); err != nil {
		return "", err
	}
	streamCtx, cancel := context.WithCancel(normalizeContext(ctx))
	sess, task := s.startStream(TaskKindBuild, ChannelBuild, contextPath, "", cancel)
	// Dir is left empty on purpose: wslc resolves the context path relative to
	// the inherited working directory, exactly like the CLI the user would run.
	spec := wslc.Spec{Kind: wslc.CmdImageBuild, Args: args, Timeout: -1 * time.Nanosecond}
	go s.pumpCommand(streamCtx, sess, spec)
	return task.ID, nil
}

// RemoveImage removes one image, optionally forcing removal of a used image.
func (s *Service) RemoveImage(ctx context.Context, ref string, force bool) (string, error) {
	target, err := requireRef("镜像引用", ref)
	if err != nil {
		return "", err
	}
	args := []string{"image", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, target)
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdImageRm, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已删除镜像 "+target), nil
}

// TagImage adds a tag to an existing image.
func (s *Service) TagImage(ctx context.Context, source, target string) (string, error) {
	from, err := requireRef("源镜像引用", source)
	if err != nil {
		return "", err
	}
	to, err := requireRef("目标镜像引用", target)
	if err != nil {
		return "", err
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdImageTag, Args: []string{"image", "tag", from, to}})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已打标签 "+from+" -> "+to), nil
}

// InspectImage returns the raw JSON `wslc image inspect` prints.
func (s *Service) InspectImage(ctx context.Context, ref string) (json.RawMessage, error) {
	target, err := requireRef("镜像引用", ref)
	if err != nil {
		return nil, err
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdImageIns, Args: []string{"image", "inspect", target}})
	if err != nil {
		return nil, err
	}
	return inspectJSON(res)
}
