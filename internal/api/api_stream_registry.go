package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *API) onStreamRegistryLookup(ctx *gin.Context) {
	name, ok := paramName(ctx)
	if !ok {
		ctx.AbortWithStatus(http.StatusBadRequest)
		return
	}

	info, err := a.StreamRegistry.Lookup(name)
	if err != nil {
		ctx.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if info == nil {
		ctx.AbortWithStatus(http.StatusNotFound)
		return
	}

	ctx.JSON(http.StatusOK, info)
}
