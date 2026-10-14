package api_router

import (

	"github.com/gin-gonic/gin"
	"github.com/haierkeys/fast-note-sync-service/internal/app"
	"github.com/haierkeys/fast-note-sync-service/internal/dto"
	pkgapp "github.com/haierkeys/fast-note-sync-service/pkg/app"
	"github.com/haierkeys/fast-note-sync-service/pkg/code"
	"github.com/haierkeys/fast-note-sync-service/pkg/fileurl"
)

// VersionHandler version info API router handler
// VersionHandler 版本信息 API 路由处理器
// Uses App Container to inject dependencies
// 使用 App Container 注入依赖
type VersionHandler struct {
	*Handler
}

// NewVersionHandler creates VersionHandler instance
// NewVersionHandler 创建 VersionHandler 实例
func NewVersionHandler(a *app.App) *VersionHandler {
	return &VersionHandler{
		Handler: NewHandler(a),
	}
}

// ServerVersion retrieves server version information
// @Summary Get server version info
// @Description Get current server software version, Git tag, and build time
// @Tags System
// @Produce json
// @Success 200 {object} pkgapp.Res{data=dto.VersionDTO} "Success"
// @Router /api/version [get]
func (h *VersionHandler) ServerVersion(c *gin.Context) {
	response := pkgapp.NewResponse(c)
	versionInfo := h.App.Version()
	checkInfo := h.App.CheckVersion("")
	response.ToResponse(code.Success.WithData(dto.VersionDTO{
		Version:                          versionInfo.Version,
		GitTag:                           versionInfo.GitTag,
		BuildTime:                        versionInfo.BuildTime,
		VersionIsNew:                     checkInfo.VersionIsNew,
		VersionNewName:                   checkInfo.VersionNewName,
		VersionNewLink:                   checkInfo.VersionNewLink,
		VersionNewChangelog:              checkInfo.VersionNewChangelog,
		VersionNewChangelogContent:       checkInfo.VersionNewChangelogContent,
		VersionHistory:                   checkInfo.VersionHistory,
		PluginVersionNewName:             checkInfo.PluginVersionNewName,
		PluginVersionNewLink:             checkInfo.PluginVersionNewLink,
		PluginVersionNewChangelog:        checkInfo.PluginVersionNewChangelog,
		PluginVersionNewChangelogContent: checkInfo.PluginVersionNewChangelogContent,
		PluginVersionHistory:             checkInfo.PluginVersionHistory,
	}))
}

// ProbeSources probes GitHub and CNB release endpoints in parallel and reports
// reachability + latency for each, plus the recommended source. Used by the
// webgui settings "test latency" panel.
// @Summary Probe version sources latency
// @Description Parallel-probe GitHub and CNB release endpoints, return reachability, latency, recommended source and current selected mode
// @Tags System
// @Produce json
// @Security UserAuthToken
// @Success 200 {object} pkgapp.Res{data=dto.SourceProbeDTO} "Success"
// @Router /api/version/probe [get]
func (h *VersionHandler) ProbeSources(c *gin.Context) {
	response := pkgapp.NewResponse(c)
	snap := h.App.SourceSelector().Probe(c.Request.Context())
	recommended := fileurl.SourceCNB
	if snap.UseGitHub {
		recommended = fileurl.SourceGitHub
	}
	response.ToResponse(code.Success.WithData(dto.SourceProbeDTO{
		GitHub: dto.SourceProbeItem{
			OK:        snap.GitHub.OK,
			LatencyMs: snap.GitHub.LatencyMs,
		},
		CNB: dto.SourceProbeItem{
			OK:        snap.CNB.OK,
			LatencyMs: snap.CNB.LatencyMs,
		},
		Recommended:  recommended,
		SelectedMode: h.App.SourceSelector().Mode(),
	}))
}
