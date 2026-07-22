package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/novitalabs/NovitaBox/internal/boxapi/response"
)

type runtimeSummaryResponse struct {
	RuntimeType  string                      `json:"runtimeType"`
	Capabilities runtimeCapabilitiesResponse `json:"capabilities"`
}

type listRuntimesResponse struct {
	Runtimes []runtimeSummaryResponse `json:"runtimes"`
}

type runtimeCapabilitiesResponse struct {
	StartFromImage    bool `json:"startFromImage"`
	StartFromTemplate bool `json:"startFromTemplate"`
	StartFromSnapshot bool `json:"startFromSnapshot"`
	Pause             bool `json:"pause"`
	Resume            bool `json:"resume"`
	FullSnapshot      bool `json:"fullSnapshot"`
	DiffSnapshot      bool `json:"diffSnapshot"`
	GPU               bool `json:"gpu"`
	Vsock             bool `json:"vsock"`
	TapNetwork        bool `json:"tapNetwork"`
	HotplugDisk       bool `json:"hotplugDisk"`
	HotplugNetwork    bool `json:"hotplugNetwork"`
	LiveResizeCPU     bool `json:"liveResizeCPU"`
	LiveResizeMemory  bool `json:"liveResizeMemory"`
	GracefulShutdown  bool `json:"gracefulShutdown"`
	SerialConsole     bool `json:"serialConsole"`
	Jailer            bool `json:"jailer"`
}

func (h *Handler) ListRuntimes(c *gin.Context) {
	response.JSON(c, http.StatusOK, listRuntimesResponse{
		Runtimes: []runtimeSummaryResponse{
			{RuntimeType: "firecracker", Capabilities: firecrackerRuntimeCapabilities()},
			{RuntimeType: "gvisor", Capabilities: gvisorRuntimeCapabilities()},
			{RuntimeType: "cloud-hypervisor", Capabilities: cloudHypervisorRuntimeCapabilities()},
		},
	})
}

func (h *Handler) GetRuntime(c *gin.Context) {
	runtimeType := normalizeRuntimeName(c.Param("runtime_type"))
	caps, ok := runtimeCapabilities(runtimeType)
	if !ok {
		response.Error(c, response.ErrNotFound("runtime not found"))
		return
	}
	response.JSON(c, http.StatusOK, runtimeSummaryResponse{
		RuntimeType:  runtimeType,
		Capabilities: caps,
	})
}

func (h *Handler) GetRuntimeCapabilities(c *gin.Context) {
	runtimeType := normalizeRuntimeName(c.Param("runtime_type"))
	caps, ok := runtimeCapabilities(runtimeType)
	if !ok {
		response.Error(c, response.ErrNotFound("runtime not found"))
		return
	}
	response.JSON(c, http.StatusOK, caps)
}

func runtimeCapabilities(runtimeType string) (runtimeCapabilitiesResponse, bool) {
	switch runtimeType {
	case "firecracker":
		return firecrackerRuntimeCapabilities(), true
	case "cloud-hypervisor":
		return cloudHypervisorRuntimeCapabilities(), true
	case "gvisor":
		return gvisorRuntimeCapabilities(), true
	default:
		return runtimeCapabilitiesResponse{}, false
	}
}

func normalizeRuntimeName(runtimeType string) string {
	switch strings.ReplaceAll(strings.ToLower(strings.TrimSpace(runtimeType)), "_", "-") {
	case "runtime-type-firecracker", "firecracker":
		return "firecracker"
	case "runtime-type-cloud-hypervisor", "cloud-hypervisor":
		return "cloud-hypervisor"
	case "runtime-type-container", "container", "gvisor":
		return "gvisor"
	default:
		return ""
	}
}

func firecrackerRuntimeCapabilities() runtimeCapabilitiesResponse {
	return runtimeCapabilitiesResponse{
		StartFromImage:    true,
		StartFromTemplate: true,
		StartFromSnapshot: true,
		Pause:             true,
		Resume:            true,
		FullSnapshot:      true,
		DiffSnapshot:      false,
		GPU:               false,
		Vsock:             true,
		TapNetwork:        true,
		GracefulShutdown:  true,
		SerialConsole:     true,
		Jailer:            true,
	}
}

func cloudHypervisorRuntimeCapabilities() runtimeCapabilitiesResponse {
	caps := firecrackerRuntimeCapabilities()
	caps.GPU = true
	caps.HotplugDisk = true
	caps.HotplugNetwork = true
	return caps
}

func gvisorRuntimeCapabilities() runtimeCapabilitiesResponse {
	return runtimeCapabilitiesResponse{
		StartFromImage:    true,
		StartFromTemplate: true,
		StartFromSnapshot: false,
		Pause:             false,
		Resume:            false,
		FullSnapshot:      false,
		DiffSnapshot:      false,
		GPU:               true,
		Vsock:             false,
		TapNetwork:        false,
		GracefulShutdown:  true,
		SerialConsole:     false,
		Jailer:            false,
	}
}
