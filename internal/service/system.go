package service

import (
	"context"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// PruneContainers removes every stopped container.
//
// `wslc system prune` does not exist in 3.0.1 (it answers
// `无法识别的命令:"prune"`), so container cleanup goes through
// `container prune`. -f is mandatory here: without it wslc waits for an
// interactive confirmation that a GUI user can never answer.
func (s *Service) PruneContainers(ctx context.Context) (PruneResult, error) {
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerPrune, Args: []string{"container", "prune", "-f"}})
	if err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Stdout: outputOr(res, "")}, nil
}

// PruneImages removes dangling images, or every unused image when all is set.
func (s *Service) PruneImages(ctx context.Context, all bool) (PruneResult, error) {
	args := []string{"image", "prune"}
	if all {
		args = append(args, "--all")
	}
	args = append(args, "-f")
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdImagePrune, Args: args})
	if err != nil {
		return PruneResult{}, err
	}
	return PruneResult{Stdout: outputOr(res, "")}, nil
}
