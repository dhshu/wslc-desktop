package service

import (
	"context"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// ListVolumes lists the session's volumes.
func (s *Service) ListVolumes(ctx context.Context) ([]domain.Volume, error) {
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdVolumeList, Args: []string{"volume", "list", "--format", "json"}})
	if err != nil {
		return nil, err
	}
	return wslc.ParseVolumes(res.Stdout)
}

// CreateVolume creates a named volume, optionally with an explicit driver.
func (s *Service) CreateVolume(ctx context.Context, name, driver string) (string, error) {
	volume, err := requireName("卷名称", name)
	if err != nil {
		return "", err
	}
	driverName, err := validateDriver(driver)
	if err != nil {
		return "", err
	}
	args := []string{"volume", "create"}
	if driverName != "" {
		args = append(args, "--driver", driverName)
	}
	args = append(args, volume)
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdVolumeCreate, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, volume), nil
}

// RemoveVolume removes one volume. force only suppresses "no such volume".
func (s *Service) RemoveVolume(ctx context.Context, name string, force bool) (string, error) {
	volume, err := requireRef("卷名称", name)
	if err != nil {
		return "", err
	}
	args := []string{"volume", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, volume)
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdVolumeRm, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已删除卷 "+volume), nil
}

// ListNetworks lists the session's networks.
func (s *Service) ListNetworks(ctx context.Context) ([]domain.Network, error) {
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdNetworkList, Args: []string{"network", "list", "--format", "json"}})
	if err != nil {
		return nil, err
	}
	return wslc.ParseNetworks(res.Stdout)
}

// CreateNetwork creates a network.
//
// Argument order is frozen: network create [--driver D] [--subnet CIDR]
// [--gateway IP] [--internal] NAME. Empty values produce no flag at all.
func (s *Service) CreateNetwork(ctx context.Context, name, driver, subnet, gateway string, internal bool) (string, error) {
	network, err := requireName("网络名称", name)
	if err != nil {
		return "", err
	}
	driverName, err := validateDriver(driver)
	if err != nil {
		return "", err
	}
	subnetCIDR, err := validateSubnet(subnet)
	if err != nil {
		return "", err
	}
	gatewayIP, err := validateGateway(gateway)
	if err != nil {
		return "", err
	}

	args := []string{"network", "create"}
	if driverName != "" {
		args = append(args, "--driver", driverName)
	}
	if subnetCIDR != "" {
		args = append(args, "--subnet", subnetCIDR)
	}
	if gatewayIP != "" {
		args = append(args, "--gateway", gatewayIP)
	}
	if internal {
		args = append(args, "--internal")
	}
	args = append(args, network)

	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdNetworkCreate, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, network), nil
}

// RemoveNetwork removes one network. force only suppresses "no such network".
func (s *Service) RemoveNetwork(ctx context.Context, name string, force bool) (string, error) {
	network, err := requireRef("网络名称", name)
	if err != nil {
		return "", err
	}
	args := []string{"network", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, network)
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdNetworkRm, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已删除网络 "+network), nil
}
