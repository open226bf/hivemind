package application

import (
	"context"

	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ClusterService aggregates a live snapshot of the platform for the dashboard:
// orchestration-cluster health and capacity (from the orchestrator) combined
// with catalog and activity counts (from the repositories).
type ClusterService struct {
	orch        ports.Orchestrator
	services    ports.ServiceRepository
	deployments ports.DeploymentRepository
	networks    ports.NetworkRepository
	secrets     ports.SecretRepository
	configs     ports.ConfigRepository
}

func NewClusterService(
	orch ports.Orchestrator,
	services ports.ServiceRepository,
	deployments ports.DeploymentRepository,
	networks ports.NetworkRepository,
	secrets ports.SecretRepository,
	configs ports.ConfigRepository,
) *ClusterService {
	return &ClusterService{
		orch:        orch,
		services:    services,
		deployments: deployments,
		networks:    networks,
		secrets:     secrets,
		configs:     configs,
	}
}

// Overview is the aggregated dashboard payload.
type Overview struct {
	Cluster  ClusterSummary
	Nodes    []ports.NodeInfo
	Services ServiceSummary
	Activity ActivitySummary
	Catalog  CatalogSummary
}

// ClusterSummary holds cluster-wide aggregates derived from the node list.
type ClusterSummary struct {
	Reachable     bool // false when the orchestrator could not be queried
	NodeTotal     int
	Managers      int
	Workers       int
	ReadyNodes    int
	TotalCPUs     float64
	TotalMemory   int64
	LeaderHost    string
	EngineVersion string
}

// ServiceSummary breaks the service catalog down by lifecycle status.
type ServiceSummary struct {
	Total    int64
	Draft    int64
	Deployed int64
	Removed  int64
}

// ActivitySummary counts deployments by terminal/active status.
type ActivitySummary struct {
	TotalDeployments int64
	InProgress       int64
	Succeeded        int64
	Failed           int64
}

// CatalogSummary counts the managed resource catalogs.
type CatalogSummary struct {
	Networks int64
	Secrets  int64
	Configs  int64
}

// Overview assembles the dashboard snapshot. Cluster health is best-effort: if
// the orchestrator is unavailable the rest of the payload (DB-sourced counts)
// is still returned with Cluster.Reachable=false, so the dashboard degrades
// gracefully rather than failing wholesale.
func (s *ClusterService) Overview(ctx context.Context) (*Overview, error) {
	ov := &Overview{}

	if s.orch != nil {
		if info, err := s.orch.ClusterInfo(ctx); err == nil {
			ov.Nodes = info.Nodes
			ov.Cluster = summarizeNodes(info.Nodes)
			ov.Cluster.Reachable = true
		}
	}

	svc, err := s.serviceSummary(ctx)
	if err != nil {
		return nil, err
	}
	ov.Services = svc

	act, err := s.activitySummary(ctx)
	if err != nil {
		return nil, err
	}
	ov.Activity = act

	cat, err := s.catalogSummary(ctx)
	if err != nil {
		return nil, err
	}
	ov.Catalog = cat

	return ov, nil
}

func summarizeNodes(nodes []ports.NodeInfo) ClusterSummary {
	cs := ClusterSummary{NodeTotal: len(nodes)}
	for _, n := range nodes {
		cs.TotalCPUs += n.CPUs
		cs.TotalMemory += n.MemoryBytes
		if n.Role == "manager" {
			cs.Managers++
		} else {
			cs.Workers++
		}
		if n.State == "ready" {
			cs.ReadyNodes++
		}
		if n.Leader {
			cs.LeaderHost = n.Hostname
			cs.EngineVersion = n.EngineVersion
		}
	}
	return cs
}

func (s *ClusterService) serviceSummary(ctx context.Context) (ServiceSummary, error) {
	total, err := s.countServices(ctx, "")
	if err != nil {
		return ServiceSummary{}, err
	}
	draft, err := s.countServices(ctx, string(service.StatusDraft))
	if err != nil {
		return ServiceSummary{}, err
	}
	deployed, err := s.countServices(ctx, string(service.StatusDeployed))
	if err != nil {
		return ServiceSummary{}, err
	}
	removed, err := s.countServices(ctx, string(service.StatusRemoved))
	if err != nil {
		return ServiceSummary{}, err
	}
	return ServiceSummary{Total: total, Draft: draft, Deployed: deployed, Removed: removed}, nil
}

func (s *ClusterService) activitySummary(ctx context.Context) (ActivitySummary, error) {
	total, err := s.countDeployments(ctx, "")
	if err != nil {
		return ActivitySummary{}, err
	}
	inProgress, err := s.countDeployments(ctx, string(deployment.StatusInProgress))
	if err != nil {
		return ActivitySummary{}, err
	}
	succeeded, err := s.countDeployments(ctx, string(deployment.StatusSucceeded))
	if err != nil {
		return ActivitySummary{}, err
	}
	failed, err := s.countDeployments(ctx, string(deployment.StatusFailed))
	if err != nil {
		return ActivitySummary{}, err
	}
	return ActivitySummary{TotalDeployments: total, InProgress: inProgress, Succeeded: succeeded, Failed: failed}, nil
}

func (s *ClusterService) catalogSummary(ctx context.Context) (CatalogSummary, error) {
	_, networks, err := s.networks.List(ctx, countPage())
	if err != nil {
		return CatalogSummary{}, err
	}
	_, secrets, err := s.secrets.List(ctx, countPage())
	if err != nil {
		return CatalogSummary{}, err
	}
	_, configs, err := s.configs.List(ctx, countPage())
	if err != nil {
		return CatalogSummary{}, err
	}
	return CatalogSummary{Networks: networks, Secrets: secrets, Configs: configs}, nil
}

func (s *ClusterService) countServices(ctx context.Context, status string) (int64, error) {
	_, total, err := s.services.List(ctx, ports.ServiceFilter{Status: status}, countPage())
	return total, err
}

func (s *ClusterService) countDeployments(ctx context.Context, status string) (int64, error) {
	_, total, err := s.deployments.List(ctx, ports.DeploymentFilter{Status: status}, countPage())
	return total, err
}

// countPage requests a single row: we only consume the total count the
// repositories return alongside the page.
func countPage() pagination.Page {
	return pagination.Page{Number: 1, Size: 1}
}
