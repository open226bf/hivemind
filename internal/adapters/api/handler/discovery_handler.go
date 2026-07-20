package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/acl"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/user"
)

// DiscoveryHandler exposes brownfield service discovery (ADR 0004): the live
// Swarm services on the active cluster, classified as managed / foreign / orphan.
type DiscoveryHandler struct {
	svc *application.DiscoveryService
	cfg middleware.ACLConfig
}

func NewDiscoveryHandler(svc *application.DiscoveryService, cfg middleware.ACLConfig) *DiscoveryHandler {
	return &DiscoveryHandler{svc: svc, cfg: cfg}
}

// Register wires discovery routes. Listing is Viewer-level (like other
// Swarm-discovery endpoints, e.g. GET /networks/swarm). Adopt/release mutate
// state: a global Operator floor plus, under ACL enforcement, write on the
// target hive — checked in the handler since the hive comes from the body /
// the owning record, not a URL param (ADR 0003/0004).
func (h *DiscoveryHandler) Register(protected *gin.RouterGroup) {
	g := protected.Group("/discovered-services")
	g.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	g.POST("/:swarmId/adopt", middleware.RequireRole(user.RoleOperator), h.Adopt)
	g.POST("/:swarmId/release", middleware.RequireRole(user.RoleOperator), h.Release)

	// Supervision of services Hivemind does NOT manage: read their logs and force
	// a restart. Both address the service by Swarm id, so they work without a
	// Hivemind record. Authorized on the cluster (an unmanaged service has no
	// hive): read for logs, write for restart.
	g.GET("/:swarmId/logs", middleware.RequireRole(user.RoleViewer), h.Logs)
	g.POST("/:swarmId/restart", middleware.RequireRole(user.RoleOperator), h.Restart)
}

// Logs godoc
//
//	@Summary		Stream a discovered service's logs (SSE)
//	@Description	Streams the aggregated container logs of any service running on the cluster, addressed by Swarm id — including services Hivemind does not manage (foreign / orphan). Each line is a `data:` event; an `event: end` frame closes a non-follow stream. Authenticate with a Bearer token (use fetch/ReadableStream, not EventSource).
//	@Tags			discovery
//	@Security		BearerAuth
//	@Produce		text/event-stream
//	@Param			swarmId		path	string	true	"Swarm service ID"
//	@Param			follow		query	bool	false	"Keep the stream open (default true)"
//	@Param			tail		query	string	false	"Number of trailing lines, or 'all' (default 200)"
//	@Param			timestamps	query	bool	false	"Prefix each line with an RFC3339 timestamp"
//	@Param			since		query	string	false	"Only logs since this time (RFC3339 or duration like 10m)"
//	@Success		200	{string}	string	"text/event-stream of log lines"
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse	"no such Swarm service"
//	@Failure		503	{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/discovered-services/{swarmId}/logs [get]
func (h *DiscoveryHandler) Logs(c *gin.Context) {
	cluster := currentCluster(c)
	if !middleware.AuthorizeVerb(c, h.cfg, cluster, uuid.Nil, acl.VerbRead) {
		return
	}
	stream, err := h.svc.ServiceLogs(c.Request.Context(), cluster, c.Param("swarmId"), parseLogOptions(c))
	if err != nil {
		writeError(c, err, "service not found")
		return
	}
	defer func() { _ = stream.Close() }()

	streamLogs(c, stream)
}

// Restart godoc
//
//	@Summary		Force-restart a discovered service
//	@Description	Rolls every task of a live Swarm service and re-pulls the image, WITHOUT changing its spec. The service's own definition is reused verbatim, so the secrets, configs, mounts, networks and environment it already uses keep working exactly as before — Hivemind only asks Swarm to recreate the tasks. Intended for services Hivemind does not manage; managed services should be redeployed through their own deployment flow.
//	@Tags			discovery
//	@Security		BearerAuth
//	@Produce		json
//	@Param			swarmId	path		string	true	"Swarm service ID"
//	@Success		202		{object}	map[string]string	"restart accepted"
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse	"no such Swarm service"
//	@Failure		503		{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/discovered-services/{swarmId}/restart [post]
func (h *DiscoveryHandler) Restart(c *gin.Context) {
	cluster := writeCluster(c)
	if !middleware.AuthorizeVerb(c, h.cfg, cluster, uuid.Nil, acl.VerbWrite) {
		return
	}
	if err := h.svc.Restart(c.Request.Context(), cluster, c.Param("swarmId")); err != nil {
		writeError(c, err, "service not found")
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "restarting"})
}

// List godoc
//
//	@Summary		List services running on the cluster (brownfield discovery)
//	@Description	Returns every Swarm service on the active cluster, each classified as managed (owned by Hivemind), foreign (created out-of-band), or orphan (labelled but unknown). Read-only; nothing is mutated.
//	@Tags			discovery
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		dto.DiscoveredService
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		503	{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/discovered-services [get]
func (h *DiscoveryHandler) List(c *gin.Context) {
	items, err := h.svc.Discover(c.Request.Context(), currentCluster(c))
	if err != nil {
		writeError(c, err, "cluster not found")
		return
	}
	out := make([]dto.DiscoveredService, len(items))
	for i, d := range items {
		out[i] = dto.DiscoveredService{
			SwarmServiceID: d.SwarmServiceID,
			Name:           d.Name,
			Image:          d.Image,
			Replicas:       d.Replicas,
			Class:          d.Class,
			CreatedAt:      d.CreatedAt,
		}
		if d.ServiceID != nil {
			out[i].ServiceID = d.ServiceID.String()
		}
		if d.HiveID != nil {
			out[i].HiveID = d.HiveID.String()
		}
	}
	c.JSON(http.StatusOK, out)
}

// Adopt godoc
//
//	@Summary		Adopt a foreign service
//	@Description	Takes over a service running on the cluster: reconstructs its spec, creates a deployed Hivemind service (optionally in a hive), seals the ownership label, and snapshots it. The live service keeps running. Returns warnings for anything not fully reconstructed.
//	@Tags			discovery
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			swarmId	path		string						true	"Swarm service ID"
//	@Param			body	body		dto.AdoptServiceRequest		false	"Target hive"
//	@Success		201		{object}	dto.AdoptServiceResponse
//	@Failure		400		{object}	dto.ErrorResponse
//	@Failure		409		{object}	dto.ErrorResponse	"already managed"
//	@Failure		422		{object}	dto.ErrorResponse	"spec cannot be adopted (e.g. invalid name)"
//	@Failure		503		{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/discovered-services/{swarmId}/adopt [post]
func (h *DiscoveryHandler) Adopt(c *gin.Context) {
	swarmID := c.Param("swarmId")
	var req dto.AdoptServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	var hiveID *uuid.UUID
	if req.HiveID != "" {
		id, ok := parseUUIDValue(c, req.HiveID, "hive_id")
		if !ok {
			return
		}
		hiveID = &id
	}

	// Adoption is a write on the target hive (ADR 0003/0004): a cluster grant
	// cascades; a hive-less adoption needs write on the cluster. The hive comes
	// from the body, so this is authorized here rather than via RequireVerb.
	hiveCoord := uuid.Nil
	if hiveID != nil {
		hiveCoord = *hiveID
	}
	if !middleware.AuthorizeVerb(c, h.cfg, writeCluster(c), hiveCoord, acl.VerbWrite) {
		return
	}

	in := application.AdoptInput{
		ClusterID:      writeCluster(c),
		SwarmServiceID: swarmID,
		HiveID:         hiveID,
	}
	if claims, ok := middleware.ClaimsFrom(c); ok {
		uid := claims.UserID
		in.UserID = &uid
	}

	res, err := h.svc.Adopt(c.Request.Context(), in)
	if err != nil {
		h.writeAdoptError(c, err)
		return
	}
	c.JSON(http.StatusCreated, dto.AdoptServiceResponse{
		ServiceID: res.ServiceID.String(),
		Warnings:  res.Warnings,
	})
}

// Release godoc
//
//	@Summary		Release an adopted service
//	@Description	Hands an adopted service back to unmanaged: clears the Hivemind ownership label and deletes the Hivemind record. The live service keeps running untouched.
//	@Tags			discovery
//	@Security		BearerAuth
//	@Param			swarmId	path	string	true	"Swarm service ID"
//	@Success		204
//	@Failure		404	{object}	dto.ErrorResponse	"no managed service owns this Swarm service"
//	@Failure		503	{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/discovered-services/{swarmId}/release [post]
func (h *DiscoveryHandler) Release(c *gin.Context) {
	cluster := writeCluster(c)
	swarmID := c.Param("swarmId")

	// Release is a write on the owning service's hive (ADR 0003/0004). Resolve the
	// hive first so the check targets it, then act.
	hive, err := h.svc.OwnedHive(c.Request.Context(), cluster, swarmID)
	if err != nil {
		if errors.Is(err, application.ErrServiceNotAdopted) {
			dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, err.Error())
			return
		}
		writeError(c, err, "service not found")
		return
	}
	hiveCoord := uuid.Nil
	if hive != nil {
		hiveCoord = *hive
	}
	if !middleware.AuthorizeVerb(c, h.cfg, cluster, hiveCoord, acl.VerbWrite) {
		return
	}

	if err := h.svc.Release(c.Request.Context(), cluster, swarmID); err != nil {
		if errors.Is(err, application.ErrServiceNotAdopted) {
			dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, err.Error())
			return
		}
		writeError(c, err, "service not found")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *DiscoveryHandler) writeAdoptError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrAlreadyManaged):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, service.ErrInvalidName), errors.Is(err, service.ErrInvalidAdoptedName),
		errors.Is(err, service.ErrInvalidImage):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		writeError(c, err, "service not found")
	}
}
