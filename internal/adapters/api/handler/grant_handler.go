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
	"github.com/open226bf/hivemind/internal/ports"
)

// GrantHandler exposes the ACL grant management API. Read/write of grants on a
// resource requires the manage verb on that resource (admins bypass).
type GrantHandler struct {
	acl      *application.AclService
	resolver middleware.ResourceResolver
	cfg      middleware.ACLConfig
}

func NewGrantHandler(acl *application.AclService, resolver middleware.ResourceResolver, cfg middleware.ACLConfig) *GrantHandler {
	return &GrantHandler{acl: acl, resolver: resolver, cfg: cfg}
}

// Register wires the grant routes. Manage on the resource is enforced by the
// RequireVerb middleware; delete authorizes inline against the grant's resource.
func (h *GrantHandler) Register(protected *gin.RouterGroup) {
	manage := acl.VerbManage
	protected.GET("/clusters/:id/grants",
		middleware.RequireVerb(middleware.TargetCluster, "id", manage, h.resolver, h.cfg), h.listFor(acl.ResourceCluster))
	protected.POST("/clusters/:id/grants",
		middleware.RequireVerb(middleware.TargetCluster, "id", manage, h.resolver, h.cfg), h.createFor(acl.ResourceCluster))
	protected.GET("/hives/:id/grants",
		middleware.RequireVerb(middleware.TargetHive, "id", manage, h.resolver, h.cfg), h.listFor(acl.ResourceHive))
	protected.POST("/hives/:id/grants",
		middleware.RequireVerb(middleware.TargetHive, "id", manage, h.resolver, h.cfg), h.createFor(acl.ResourceHive))
	protected.DELETE("/grants/:id", h.Delete)
}

func (h *GrantHandler) listFor(rt acl.ResourceType) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseUUID(c, "id")
		if !ok {
			return
		}
		grants, err := h.acl.ListByResource(c.Request.Context(), rt, id)
		if err != nil {
			dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list grants")
			return
		}
		resp := dto.GrantListResponse{Items: make([]dto.GrantResponse, 0, len(grants))}
		for _, g := range grants {
			resp.Items = append(resp.Items, toGrantResponse(g))
		}
		c.JSON(http.StatusOK, resp)
	}
}

func (h *GrantHandler) createFor(rt acl.ResourceType) gin.HandlerFunc {
	return func(c *gin.Context) {
		resourceID, ok := parseUUID(c, "id")
		if !ok {
			return
		}
		var req dto.CreateGrantRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
			return
		}
		subjectID, err := uuid.Parse(req.SubjectID)
		if err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid subject_id")
			return
		}
		granter := granterID(c)
		g, err := h.acl.Grant(c.Request.Context(), granter, subjectID, rt, resourceID, acl.Verb(req.Verb), req.ExpiresAt)
		if err != nil {
			h.writeGrantError(c, err)
			return
		}
		c.JSON(http.StatusCreated, toGrantResponse(g))
	}
}

// Delete revokes a grant. Authorized inline: the caller must hold manage on the
// grant's own resource (admins bypass).
func (h *GrantHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	g, err := h.acl.GrantByID(c.Request.Context(), id)
	if err != nil {
		writeError(c, err, "grant not found")
		return
	}
	if !h.mayManage(c, g) {
		dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, "insufficient permissions")
		return
	}
	if err := h.acl.Revoke(c.Request.Context(), granterID(c), id); err != nil {
		h.writeGrantError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// mayManage reports whether the caller can manage the grant's resource. Admins
// and the shadow-mode pass always succeed.
func (h *GrantHandler) mayManage(c *gin.Context, g *acl.Grant) bool {
	claims, ok := middleware.ClaimsFrom(c)
	if !ok {
		return false
	}
	if claims.Role == "admin" {
		return true
	}
	if !h.cfg.Enforced {
		return true
	}
	target := middleware.TargetCluster
	if g.ResourceType == acl.ResourceHive {
		target = middleware.TargetHive
	}
	clusterID, hiveID, err := h.resolver.Resolve(c.Request.Context(), target, g.ResourceID)
	if err != nil {
		return false
	}
	return ports.EffectiveVerb(claims.Scopes, clusterID, hiveID).AtLeast(acl.VerbManage)
}

func (h *GrantHandler) writeGrantError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrSelfGrant):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, acl.ErrInvalidVerb), errors.Is(err, acl.ErrInvalidResourceType):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		writeError(c, err, "subject not found")
	}
}

// granterID returns the authenticated user's id (uuid.Nil if unknown).
func granterID(c *gin.Context) uuid.UUID {
	if claims, ok := middleware.ClaimsFrom(c); ok {
		return claims.UserID
	}
	return uuid.Nil
}

func toGrantResponse(g *acl.Grant) dto.GrantResponse {
	r := dto.GrantResponse{
		ID:           g.ID.String(),
		SubjectID:    g.SubjectID.String(),
		ResourceType: string(g.ResourceType),
		ResourceID:   g.ResourceID.String(),
		Verb:         string(g.Verb),
		CreatedAt:    g.CreatedAt,
		ExpiresAt:    g.ExpiresAt,
	}
	if g.CreatedBy != uuid.Nil {
		r.CreatedBy = g.CreatedBy.String()
	}
	return r
}
