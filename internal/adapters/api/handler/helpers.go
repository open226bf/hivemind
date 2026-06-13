package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// parseUUID parses a named URL parameter as a UUID. It writes a 400 response
// and returns false if the value is not a valid UUID.
func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid "+param+": must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

// parsePage reads the `page` and `size` query parameters and returns a
// validated Page. Defaults: page=1, size=20, max size=100.
func parsePage(c *gin.Context) pagination.Page {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	return pagination.New(page, size)
}
