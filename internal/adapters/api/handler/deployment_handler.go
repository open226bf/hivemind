package handler

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

type DeploymentHandler struct {
	svc *application.DeploymentService
}

func NewDeploymentHandler(svc *application.DeploymentService) *DeploymentHandler {
	return &DeploymentHandler{svc: svc}
}

// Register wires deployment routes.
func (h *DeploymentHandler) Register(protected *gin.RouterGroup) {
	protected.POST("/services/:id/deploy", middleware.RequireRole(user.RoleOperator), h.Deploy)
	protected.POST("/services/:id/undeploy", middleware.RequireRole(user.RoleOperator), h.Undeploy)
	protected.GET("/services/:id/deployments", middleware.RequireRole(user.RoleViewer), h.ListForService)
	protected.GET("/services/:id/status", middleware.RequireRole(user.RoleViewer), h.Status)
	protected.GET("/services/:id/tasks", middleware.RequireRole(user.RoleViewer), h.Tasks)
	protected.GET("/services/:id/logs", middleware.RequireRole(user.RoleViewer), h.Logs)
	protected.GET("/deployments", middleware.RequireRole(user.RoleViewer), h.List)
	protected.GET("/deployments/:id", middleware.RequireRole(user.RoleViewer), h.Get)
}

// Deploy godoc
//
//	@Summary		Deploy a service
//	@Description	Triggers a deployment of the service to the orchestrator. Runs asynchronously: responds 202 with a pending deployment; poll GET /deployments/{id} for the outcome.
//	@Tags			deployments
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		202	{object}	dto.DeploymentResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"a deployment is already in progress"
//	@Failure		503	{object}	dto.ErrorResponse	"deployment engine not configured"
//	@Router			/services/{id}/deploy [post]
func (h *DeploymentHandler) Deploy(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	// Body is optional: an empty/missing body keeps the previous behaviour.
	var body dto.DeployRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
			return
		}
	}

	in := application.BeginDeploymentInput{
		ServiceID: serviceID,
		Trigger:   deployment.TriggerManual,
		Options: application.DeployOptions{
			Force:  body.Force,
			Repull: body.Repull,
		},
	}
	if claims, ok := middleware.ClaimsFrom(c); ok {
		uid := claims.UserID
		in.UserID = &uid
	}

	dep, err := h.svc.DeployAsync(c.Request.Context(), in)
	if err != nil {
		h.writeDeploymentError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, toDeploymentResponse(dep))
}

// Undeploy godoc
//
//	@Summary		Undeploy a service
//	@Description	Removes the service from the orchestrator (Swarm) but keeps its Hivemind definition. Status transitions deployed → removed. Idempotent: a service that is not deployed returns its current state. Use POST /services/{id}/deploy to redeploy it later.
//	@Tags			deployments
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{object}	dto.ServiceResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"a deployment is still in progress"
//	@Failure		503	{object}	dto.ErrorResponse	"deployment engine not configured"
//	@Router			/services/{id}/undeploy [post]
func (h *DeploymentHandler) Undeploy(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	svc, err := h.svc.Undeploy(c.Request.Context(), serviceID)
	if err != nil {
		h.writeDeploymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, toServiceResponse(svc))
}

// ListForService godoc
//
//	@Summary		List a service's deployments
//	@Tags			deployments
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id		path		string	true	"Service ID (UUID)"
//	@Param			page	query		int		false	"Page number (default 1)"
//	@Param			size	query		int		false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.DeploymentListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Router			/services/{id}/deployments [get]
func (h *DeploymentHandler) ListForService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	page := parsePage(c)

	items, total, err := h.svc.ListForService(c.Request.Context(), serviceID, page)
	if err != nil {
		h.writeDeploymentError(c, err)
		return
	}

	resp := dto.DeploymentListResponse{
		Items: make([]dto.DeploymentResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, d := range items {
		resp.Items[i] = toDeploymentResponse(d)
	}
	c.JSON(http.StatusOK, resp)
}

// List godoc
//
//	@Summary		List deployments (history)
//	@Description	Global deployment history, filterable by service, status and time range (RFC3339), newest first.
//	@Tags			deployments
//	@Security		BearerAuth
//	@Produce		json
//	@Param			service_id	query		string	false	"Filter by service ID (UUID)"
//	@Param			status		query		string	false	"Filter by status (pending | in_progress | succeeded | failed | rolled_back)"
//	@Param			from		query		string	false	"Started at >= (RFC3339)"
//	@Param			to			query		string	false	"Started at <= (RFC3339)"
//	@Param			page		query		int		false	"Page number (default 1)"
//	@Param			size		query		int		false	"Page size (default 20, max 100)"
//	@Success		200			{object}	dto.DeploymentListResponse
//	@Failure		400			{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401			{object}	dto.ErrorResponse
//	@Failure		403			{object}	dto.ErrorResponse
//	@Router			/deployments [get]
func (h *DeploymentHandler) List(c *gin.Context) {
	page := parsePage(c)

	var filter ports.DeploymentFilter
	if sid := c.Query("service_id"); sid != "" {
		id, err := uuid.Parse(sid)
		if err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid service_id: must be a valid UUID")
			return
		}
		filter.ServiceID = &id
	}
	if st := c.Query("status"); st != "" {
		if !deployment.Status(st).IsValid() {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid status")
			return
		}
		filter.Status = st
	}
	if from := c.Query("from"); from != "" {
		if _, err := time.Parse(time.RFC3339, from); err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid 'from': expected RFC3339")
			return
		}
		filter.From = &from
	}
	if to := c.Query("to"); to != "" {
		if _, err := time.Parse(time.RFC3339, to); err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid 'to': expected RFC3339")
			return
		}
		filter.To = &to
	}

	items, total, err := h.svc.List(c.Request.Context(), filter, page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list deployments")
		return
	}

	resp := dto.DeploymentListResponse{
		Items: make([]dto.DeploymentResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, d := range items {
		resp.Items[i] = toDeploymentResponse(d)
	}
	c.JSON(http.StatusOK, resp)
}

// Status godoc
//
//	@Summary		Live status of a service (F-MVP-10)
//	@Description	Returns the aggregated orchestrator view of a service: effective vs. desired replicas (running x/y), plus pending/failed counts and whether an update is in progress. Read in near real time from Swarm with a short cache (TTL <= 5s).
//	@Tags			supervision
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{object}	dto.ServiceStatusResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		503	{object}	dto.ErrorResponse	"deployment engine not configured"
//	@Router			/services/{id}/status [get]
func (h *DeploymentHandler) Status(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	state, err := h.svc.ServiceState(c.Request.Context(), serviceID)
	if err != nil {
		h.writeDeploymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, toServiceStatusResponse(state))
}

// Tasks godoc
//
//	@Summary		Tasks of a service (F-MVP-10)
//	@Description	Returns the per-task (container) detail of a service: executing node, current and desired state, last update and any Swarm error message. Read in near real time from Swarm with a short cache (TTL <= 5s).
//	@Tags			supervision
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{object}	dto.ServiceTasksResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		503	{object}	dto.ErrorResponse	"deployment engine not configured"
//	@Router			/services/{id}/tasks [get]
func (h *DeploymentHandler) Tasks(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	state, err := h.svc.ServiceState(c.Request.Context(), serviceID)
	if err != nil {
		h.writeDeploymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, toServiceTasksResponse(state))
}

// Logs godoc
//
//	@Summary		Stream a service's logs (SSE, F-V2-01)
//	@Description	Streams the service's aggregated container logs as Server-Sent Events. Each log line is delivered as a `data:` event; an `event: end` frame closes a non-follow stream. With follow=true the stream stays open until the client disconnects. Authenticate with a Bearer token (use fetch/ReadableStream, not EventSource).
//	@Tags			supervision
//	@Security		BearerAuth
//	@Produce		text/event-stream
//	@Param			id			path	string	true	"Service ID (UUID)"
//	@Param			follow		query	bool	false	"Keep the stream open (default true)"
//	@Param			tail		query	string	false	"Number of trailing lines, or 'all' (default 200)"
//	@Param			timestamps	query	bool	false	"Prefix each line with an RFC3339 timestamp"
//	@Param			since		query	string	false	"Only logs since this time (RFC3339 or duration like 10m)"
//	@Success		200	{string}	string	"text/event-stream of log lines"
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"service not deployed"
//	@Failure		503	{object}	dto.ErrorResponse	"deployment engine not configured"
//	@Router			/services/{id}/logs [get]
func (h *DeploymentHandler) Logs(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	opts := ports.LogOptions{
		Follow:     c.DefaultQuery("follow", "true") == "true",
		Tail:       c.DefaultQuery("tail", "200"),
		Timestamps: c.Query("timestamps") == "true",
		Since:      c.Query("since"),
	}

	stream, err := h.svc.ServiceLogs(c.Request.Context(), serviceID, opts)
	if err != nil {
		h.writeDeploymentError(c, err)
		return
	}
	defer stream.Close()

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "streaming unsupported")
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable proxy buffering (nginx)

	// Close the stream when the client disconnects so the scanner unblocks.
	ctx := c.Request.Context()
	go func() {
		<-ctx.Done()
		stream.Close()
	}()

	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", scanner.Text()); err != nil {
			return // client gone
		}
		flusher.Flush()
	}
	// Signal completion for non-follow streams.
	fmt.Fprint(c.Writer, "event: end\ndata: \n\n")
	flusher.Flush()
}

// Get godoc
//
//	@Summary		Get a deployment
//	@Tags			deployments
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Deployment ID (UUID)"
//	@Success		200	{object}	dto.DeploymentResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/deployments/{id} [get]
func (h *DeploymentHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	dep, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeDeploymentError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDeploymentResponse(dep))
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (h *DeploymentHandler) writeDeploymentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "resource not found")
	case errors.Is(err, deployment.ErrAlreadyInProgress),
		errors.Is(err, application.ErrServiceNotDeployed),
		errors.Is(err, application.ErrDeploymentInProgress):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, application.ErrOrchestratorUnavailable):
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

func toServiceStatusResponse(s *ports.ServiceState) dto.ServiceStatusResponse {
	return dto.ServiceStatusResponse{
		Running:           s.Running,
		Desired:           s.Desired,
		Pending:           s.Pending,
		Failed:            s.Failed,
		Updating:          s.Updating,
		ExternallyRemoved: s.ExternallyRemoved,
	}
}

func toServiceTasksResponse(s *ports.ServiceState) dto.ServiceTasksResponse {
	resp := dto.ServiceTasksResponse{
		Tasks: make([]dto.TaskStateResponse, len(s.Tasks)),
	}
	for i, t := range s.Tasks {
		var nets []dto.TaskNetworkDetail
		for _, n := range t.Networks {
			nets = append(nets, dto.TaskNetworkDetail{Name: n.Name, Address: n.Address})
		}
		resp.Tasks[i] = dto.TaskStateResponse{
			ID:           t.ID,
			ContainerID:  t.ContainerID,
			Node:         t.Node,
			Image:        t.Image,
			Slot:         t.Slot,
			CurrentState: t.CurrentState,
			DesiredState: t.DesiredState,
			Message:      t.Message,
			ErrorMessage: t.ErrorMessage,
			ExitCode:     t.ExitCode,
			PID:          t.PID,
			Networks:     nets,
			CreatedAt:    t.CreatedAt,
			UpdatedAt:    t.UpdatedAt,
		}
	}
	return resp
}

func toDeploymentResponse(d *deployment.Deployment) dto.DeploymentResponse {
	resp := dto.DeploymentResponse{
		ID:           d.ID.String(),
		ServiceID:    d.ServiceID.String(),
		ImageTag:     d.ImageTag,
		Trigger:      string(d.Trigger),
		Status:       string(d.Status),
		ErrorMessage: d.ErrorMessage,
		StartedAt:    d.StartedAt,
		FinishedAt:   d.FinishedAt,
	}
	if d.UserID != nil {
		resp.UserID = d.UserID.String()
	}
	if dur := d.Duration(); dur != nil {
		ms := dur.Milliseconds()
		resp.DurationMs = &ms
	}
	return resp
}
