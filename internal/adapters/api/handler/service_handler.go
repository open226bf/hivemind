package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

type ServiceHandler struct {
	svc *application.ServiceService
}

func NewServiceHandler(svc *application.ServiceService) *ServiceHandler {
	return &ServiceHandler{svc: svc}
}

// Register wires service routes onto a protected (authenticated) router group.
func (h *ServiceHandler) Register(protected *gin.RouterGroup) {
	g := protected.Group("/services")
	g.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	g.POST("", middleware.RequireRole(user.RoleOperator), h.Create)
	g.GET("/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	g.PUT("/:id", middleware.RequireRole(user.RoleOperator), h.Update)
	g.PUT("/:id/resources", middleware.RequireRole(user.RoleOperator), h.SetResources)
	g.PUT("/:id/placement", middleware.RequireRole(user.RoleOperator), h.SetPlacement)
	g.GET("/:id/env", middleware.RequireRole(user.RoleViewer), h.GetEnvVars)
	g.PUT("/:id/env", middleware.RequireRole(user.RoleOperator), h.SetEnvVars)
	g.GET("/:id/ports", middleware.RequireRole(user.RoleViewer), h.GetPorts)
	g.PUT("/:id/ports", middleware.RequireRole(user.RoleOperator), h.SetPorts)
	g.DELETE("/:id", middleware.RequireRole(user.RoleOperator), h.Delete)
}

// List godoc
//
//	@Summary		List services
//	@Description	Returns a paginated list of services. Filter by name (partial, case-insensitive) and/or status.
//	@Tags			services
//	@Security		BearerAuth
//	@Produce		json
//	@Param			name	query		string					false	"Filter by name (contains, case-insensitive)"
//	@Param			status	query		string					false	"Filter by status (draft | deployed | removed)"
//	@Param			page	query		int						false	"Page number (default 1)"
//	@Param			size	query		int						false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.ServiceListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Router			/services [get]
func (h *ServiceHandler) List(c *gin.Context) {
	page := parsePage(c)
	if st := c.Query("status"); st != "" && !service.Status(st).IsValid() {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid status: must be draft, deployed or removed")
		return
	}
	filter := ports.ServiceFilter{
		Name:   c.Query("name"),
		Status: c.Query("status"),
	}
	if c.Query("unassigned") == "true" {
		filter.Unassigned = true
	} else if hid := c.Query("hive_id"); hid != "" {
		id, err := uuid.Parse(hid)
		if err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid hive_id")
			return
		}
		filter.HiveID = &id
	}
	if cid := currentCluster(c); cid != uuid.Nil {
		filter.ClusterID = &cid
	}

	items, total, err := h.svc.List(c.Request.Context(), filter, page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list services")
		return
	}

	resp := dto.ServiceListResponse{
		Items: make([]dto.ServiceResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, s := range items {
		resp.Items[i] = toServiceResponse(s)
	}
	c.JSON(http.StatusOK, resp)
}

// Create godoc
//
//	@Summary		Create a service
//	@Description	Saves a new service in draft status. Does NOT deploy it — use POST /services/{id}/deploy for that.
//	@Tags			services
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.CreateServiceRequest	true	"Service definition"
//	@Success		201		{object}	dto.ServiceResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		409		{object}	dto.ErrorResponse	"name already taken"
//	@Failure		422		{object}	dto.ErrorResponse	"invalid name format or resource constraint"
//	@Router			/services [post]
func (h *ServiceHandler) Create(c *gin.Context) {
	var req dto.CreateServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	hiveID := uuid.Nil
	if req.Hive != "" {
		id, err := uuid.Parse(req.Hive)
		if err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid hive_id")
			return
		}
		hiveID = id
	}

	clusterID := currentCluster(c) // active cluster from X-Hivemind-Cluster

	in := application.CreateServiceInput{
		Name:        req.Name,
		Description: req.Description,
		Image:       req.Image,
		Tag:         req.Tag,
		Replicas:    req.Replicas,
		Command:     req.Command,
		Entrypoint:  req.Entrypoint,
	}
	if req.Resources != nil {
		in.Resources = fromResourcesDTO(*req.Resources)
	}
	if req.Placement != nil {
		in.Placement = fromPlacementDTO(*req.Placement)
	}
	if req.UpdateConfig != nil {
		uc := fromUpdateConfigDTO(*req.UpdateConfig)
		in.UpdateConfig = &uc
	}

	if hiveID != uuid.Nil {
		in.Hive = hiveID
	}
	in.Cluster = clusterID

	svc, err := h.svc.Create(c.Request.Context(), in)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toServiceResponse(svc))
}

// Get godoc
//
//	@Summary		Get a service
//	@Description	Returns the full service definition and its current status.
//	@Tags			services
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{object}	dto.ServiceResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/services/{id} [get]
func (h *ServiceHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	svc, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toServiceResponse(svc))
}

// Update godoc
//
//	@Summary		Update a service
//	@Description	Updates mutable service fields. The service name is immutable. Only non-null fields are changed.
//	@Tags			services
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Service ID (UUID)"
//	@Param			body	body		dto.UpdateServiceRequest	true	"Fields to update (null = unchanged)"
//	@Success		200		{object}	dto.ServiceResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse	"invalid resource constraint"
//	@Router			/services/{id} [put]
func (h *ServiceHandler) Update(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var req dto.UpdateServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	in := application.UpdateServiceInput{
		Description: req.Description,
		Image:       req.Image,
		Tag:         req.Tag,
		Replicas:    req.Replicas,
		Command:     req.Command,
		Entrypoint:  req.Entrypoint,
	}
	if req.Resources != nil {
		r := fromResourcesDTO(*req.Resources)
		in.Resources = &r
	}
	if req.Placement != nil {
		p := fromPlacementDTO(*req.Placement)
		in.Placement = &p
	}
	if req.UpdateConfig != nil {
		uc := fromUpdateConfigDTO(*req.UpdateConfig)
		in.UpdateConfig = &uc
	}

	svc, err := h.svc.Update(c.Request.Context(), id, in)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toServiceResponse(svc))
}

// SetResources godoc
//
//	@Summary		Set a service's CPU/memory constraints
//	@Description	Updates only the resource reservations and limits (F-MVP-03). CPU is in decimal cores, memory in bytes. A limit of 0 means "unbounded"; a non-zero limit must be >= its reservation.
//	@Tags			services
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"Service ID (UUID)"
//	@Param			body	body		dto.ResourcesDTO	true	"CPU/memory constraints"
//	@Success		200		{object}	dto.ServiceResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse	"limit below reservation or negative value"
//	@Router			/services/{id}/resources [put]
func (h *ServiceHandler) SetResources(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var req dto.ResourcesDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	svc, err := h.svc.SetResources(c.Request.Context(), id, fromResourcesDTO(req))
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toServiceResponse(svc))
}

// SetPlacement godoc
//
//	@Summary		Set a service's placement
//	@Description	Updates only the scheduling placement: hard constraints (e.g. "node.role==worker"), spread preferences (e.g. "node.labels.zone") and the max replicas per node (0 = unlimited). Leaves every other field unchanged.
//	@Tags			services
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"Service ID (UUID)"
//	@Param			body	body		dto.PlacementDTO	true	"Placement rules"
//	@Success		200		{object}	dto.ServiceResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse	"malformed constraint or empty preference"
//	@Router			/services/{id}/placement [put]
func (h *ServiceHandler) SetPlacement(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var req dto.PlacementDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	svc, err := h.svc.SetPlacement(c.Request.Context(), id, fromPlacementDTO(req))
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toServiceResponse(svc))
}

// GetEnvVars godoc
//
//	@Summary		List a service's environment variables
//	@Description	Returns the env vars of a service. Secret values are masked (empty string) and never returned in clear text.
//	@Tags			services
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{object}	dto.EnvVarsResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/services/{id}/env [get]
func (h *ServiceHandler) GetEnvVars(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	vars, err := h.svc.GetEnvVars(c.Request.Context(), id)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toEnvVarsResponse(vars))
}

// SetEnvVars godoc
//
//	@Summary		Replace a service's environment variables
//	@Description	Atomically replaces the full set of env vars (F-MVP-04). The submitted list is authoritative — omitted keys are removed. Keys must match ^[A-Z_][A-Z0-9_]*$ and be unique. Secret values are encrypted at rest.
//	@Tags			services
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Service ID (UUID)"
//	@Param			body	body		dto.SetEnvVarsRequest	true	"Full env var set"
//	@Success		200		{object}	dto.EnvVarsResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse	"invalid or duplicate key"
//	@Router			/services/{id}/env [put]
func (h *ServiceHandler) SetEnvVars(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var req dto.SetEnvVarsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	inputs := make([]application.EnvVarInput, len(req.Vars))
	for i, v := range req.Vars {
		inputs[i] = application.EnvVarInput{Key: v.Key, Value: v.Value, IsSecret: v.IsSecret}
	}

	vars, err := h.svc.SetEnvVars(c.Request.Context(), id, inputs)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toEnvVarsResponse(vars))
}

// GetPorts godoc
//
//	@Summary		List a service's published ports
//	@Tags			services
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{object}	dto.PortsResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/services/{id}/ports [get]
func (h *ServiceHandler) GetPorts(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	pts, err := h.svc.GetPorts(c.Request.Context(), id)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toPortsResponse(pts))
}

// SetPorts godoc
//
//	@Summary		Replace a service's published ports
//	@Description	Atomically replaces the full set of published ports. The submitted list is authoritative — omitted ports are removed. Applied at the next deploy.
//	@Tags			services
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"Service ID (UUID)"
//	@Param			body	body		dto.SetPortsRequest	true	"Full published-port set"
//	@Success		200		{object}	dto.PortsResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse	"invalid port"
//	@Router			/services/{id}/ports [put]
func (h *ServiceHandler) SetPorts(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	var req dto.SetPortsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	pts := make([]service.Port, len(req.Ports))
	for i, p := range req.Ports {
		pts[i] = service.Port{
			TargetPort:    p.TargetPort,
			PublishedPort: p.PublishedPort,
			Protocol:      p.Protocol,
			Mode:          p.Mode,
		}
	}

	saved, err := h.svc.SetPorts(c.Request.Context(), id, pts)
	if err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, toPortsResponse(saved))
}

// Delete godoc
//
//	@Summary		Delete a service
//	@Description	Deletes a draft or removed service. Deleting a deployed service requires the deployment engine (F-MVP-08) to be active; until then, undeploy the service first.
//	@Tags			services
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Service ID (UUID)"
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"service is deployed"
//	@Router			/services/{id} [delete]
func (h *ServiceHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}

	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Error mapping ────────────────────────────────────────────────────────────

func (h *ServiceHandler) writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "service not found")
	case errors.Is(err, domainerrors.ErrConflict), errors.Is(err, application.ErrServiceDeployed):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case isValidationError(err):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

// isValidationError reports whether err is a domain invariant violation that
// should surface as 422 Unprocessable Entity.
func isValidationError(err error) bool {
	switch {
	case errors.Is(err, service.ErrInvalidName),
		errors.Is(err, service.ErrInvalidImage),
		errors.Is(err, service.ErrResourceConflict),
		errors.Is(err, service.ErrNegativeResource),
		errors.Is(err, application.ErrResourceExceedsCluster),
		errors.Is(err, service.ErrInvalidFailureAction),
		errors.Is(err, service.ErrInvalidOrder),
		errors.Is(err, service.ErrInvalidFailureRatio),
		errors.Is(err, service.ErrInvalidConstraint),
		errors.Is(err, service.ErrInvalidPreference),
		errors.Is(err, service.ErrInvalidEnvKey),
		errors.Is(err, service.ErrDuplicateKey),
		errors.Is(err, service.ErrInvalidPortTarget),
		errors.Is(err, service.ErrInvalidPortPublished),
		errors.Is(err, service.ErrInvalidPortProtocol),
		errors.Is(err, service.ErrInvalidPortMode),
		errors.Is(err, service.ErrDuplicatePort):
		return true
	default:
		return false
	}
}

// ─── DTO converters ───────────────────────────────────────────────────────────

func toPortsResponse(pts []service.Port) dto.PortsResponse {
	out := dto.PortsResponse{Ports: make([]dto.PortDTO, len(pts))}
	for i, p := range pts {
		out.Ports[i] = dto.PortDTO{
			TargetPort:    p.TargetPort,
			PublishedPort: p.PublishedPort,
			Protocol:      p.Protocol,
			Mode:          p.Mode,
		}
	}
	return out
}

func toServiceResponse(s *service.Service) dto.ServiceResponse {
	hiveID := ""
	if s.HiveID != nil {
		hiveID = s.HiveID.String()
	}
	clusterID := ""
	if s.ClusterID != uuid.Nil {
		clusterID = s.ClusterID.String()
	}
	return dto.ServiceResponse{
		ID:             s.ID.String(),
		ClusterID:      clusterID,
		HiveID:         hiveID,
		Name:           s.Name,
		Description:    s.Description,
		Image:          s.Image,
		Tag:            s.Tag,
		FullImage:      s.FullImage(),
		Replicas:       s.Replicas,
		Command:        nullSafeStrings(s.Command),
		Entrypoint:     nullSafeStrings(s.Entrypoint),
		Resources:      toResourcesDTO(s.Resources),
		Placement:      toPlacementDTO(s.Placement),
		UpdateConfig:   toUpdateConfigDTO(s.UpdateConfig),
		Status:         string(s.Status),
		SwarmServiceID: s.SwarmServiceID,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

func toResourcesDTO(r service.Resources) dto.ResourcesDTO {
	return dto.ResourcesDTO{
		CPUReservation: r.CPUReservation,
		CPULimit:       r.CPULimit,
		MemReservation: r.MemReservation,
		MemLimit:       r.MemLimit,
	}
}

func fromResourcesDTO(r dto.ResourcesDTO) service.Resources {
	return service.Resources{
		CPUReservation: r.CPUReservation,
		CPULimit:       r.CPULimit,
		MemReservation: r.MemReservation,
		MemLimit:       r.MemLimit,
	}
}

func toPlacementDTO(p service.Placement) dto.PlacementDTO {
	return dto.PlacementDTO{
		Constraints:        nullSafeStrings(p.Constraints),
		Preferences:        nullSafeStrings(p.Preferences),
		MaxReplicasPerNode: p.MaxReplicas,
	}
}

func fromPlacementDTO(p dto.PlacementDTO) service.Placement {
	return service.Placement{
		Constraints: trimmedNonEmpty(p.Constraints),
		Preferences: trimmedNonEmpty(p.Preferences),
		MaxReplicas: p.MaxReplicasPerNode,
	}
}

func toUpdateConfigDTO(uc service.UpdateConfig) dto.UpdateConfigDTO {
	return dto.UpdateConfigDTO{
		Parallelism:     uc.Parallelism,
		DelaySeconds:    int64(uc.Delay / time.Second),
		FailureAction:   uc.FailureAction,
		MonitorSeconds:  int64(uc.Monitor / time.Second),
		MaxFailureRatio: uc.MaxFailureRatio,
		Order:           uc.Order,
	}
}

func fromUpdateConfigDTO(uc dto.UpdateConfigDTO) service.UpdateConfig {
	return service.UpdateConfig{
		Parallelism:     uc.Parallelism,
		Delay:           time.Duration(uc.DelaySeconds) * time.Second,
		FailureAction:   uc.FailureAction,
		Monitor:         time.Duration(uc.MonitorSeconds) * time.Second,
		MaxFailureRatio: uc.MaxFailureRatio,
		Order:           uc.Order,
	}
}

// toEnvVarsResponse maps domain env vars to the API response, masking secret
// values so they are never returned in clear text.
func toEnvVarsResponse(vars []service.EnvVar) dto.EnvVarsResponse {
	out := make([]dto.EnvVarDTO, len(vars))
	for i, v := range vars {
		value := v.Value
		if v.IsSecret {
			value = "" // masked — never echo secret values
		}
		out[i] = dto.EnvVarDTO{Key: v.Key, Value: value, IsSecret: v.IsSecret}
	}
	return dto.EnvVarsResponse{Vars: out, Count: len(out)}
}

// nullSafeStrings returns an empty (non-nil) slice when s is nil so that
// the JSON response always emits [] rather than null for array fields.
func nullSafeStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// trimmedNonEmpty trims each entry and drops blank ones, so stray empty rows
// from the UI never reach domain validation as malformed rules.
func trimmedNonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}
