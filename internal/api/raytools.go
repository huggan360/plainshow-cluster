package api

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/huggan360/plainshow-cluster/internal/config"
	"github.com/huggan360/plainshow-cluster/internal/projectfs"
	"github.com/huggan360/plainshow-cluster/internal/ray"
)

func (s *Server) testProjectRay(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	head := s.rayHead(p.NetworkID)
	if head == "" {
		fail(w, 409, "Start Ray from this project's network, then test again.")
		return
	}
	nodes, err := s.liveNodes(p.NetworkID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	results, err := ray.Diagnose(ctx, ray.DashboardURL(hostOf(head), ray.DefaultDashboard), "plainshow_test_"+config.NewID())
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	type deviceCheck struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Online bool   `json:"online"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	checks := []deviceCheck{}
	ok := len(results) > 0
	for _, result := range results {
		if !result.OK {
			ok = false
		}
	}
	for _, node := range nodes {
		check := deviceCheck{ID: node.NodeID, Name: node.Name, Online: nodeCapacityOnline(node, time.Now())}
		if !check.Online {
			check.Detail = "Offline · skipped"
			checks = append(checks, check)
			continue
		}
		endpoint, _ := url.Parse(node.Address)
		if endpoint != nil {
			for _, result := range results {
				if result.Address == endpoint.Hostname() {
					check.OK = result.OK
					check.Detail = result.Error
					break
				}
			}
		}
		if check.OK {
			check.Detail = "Executed a task on this device"
		} else if check.Detail == "" {
			check.Detail = "Not attached to this Ray cluster. Check its compute network and worker policy."
		}
		if !check.OK {
			ok = false
		}
		checks = append(checks, check)
	}
	writeJSON(w, 200, map[string]any{"ok": ok, "devices": checks, "workers": results, "checked_at": time.Now().UTC(), "detail": "Checks Ray task execution; GPU drivers and your training code are not exercised."})
}

func (s *Server) createRayPreset(w http.ResponseWriter, r *http.Request) {
	p, fsys, err := s.project(r.PathValue("name"))
	if err != nil {
		fail(w, 404, "No such project.")
		return
	}
	var body struct {
		Mode     string `json:"mode"`
		Filename string `json:"filename"`
		Limit    int    `json:"limit"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	body.Filename = strings.TrimSpace(body.Filename)
	if !projectfs.ValidName(body.Filename) || !strings.HasSuffix(body.Filename, ".py") {
		fail(w, 400, "Use a Python filename, such as ray_cpu.py.")
		return
	}
	code, err := ray.Preset(body.Mode, p.ID, body.Filename, body.Limit)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	head := s.rayHead(p.NetworkID)
	dashboard := ray.DashboardURL(hostOf(head), ray.DefaultDashboard)
	if head == "" {
		head = "auto"
		dashboard = "http://YOUR_RAY_HEAD:8265"
	}
	code = strings.NewReplacer("__HEAD__", head, "__DASHBOARD__", dashboard).Replace(code)
	target, err := fsys.Resolve(body.Filename)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0640)
	if os.IsExist(err) {
		fail(w, 409, "That file already exists. Choose another name to keep your code.")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if err := projectfs.InheritOwner(target, fsys.Root); err != nil {
		file.Close()
		fail(w, 500, err.Error())
		return
	}
	_, err = file.WriteString(code)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s.hub.Publish("tree.changed", map[string]string{"project_id": p.ID, "project": p.Name})
	writeJSON(w, 201, map[string]string{"path": body.Filename, "code": code})
}
