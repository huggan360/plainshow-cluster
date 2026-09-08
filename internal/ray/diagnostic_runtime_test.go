package ray

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestDiagnosticSoftwareFailuresRemainInformational(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	const harness = `
import contextlib, io, json, sys, types
config = json.load(sys.stdin)
ray = types.ModuleType("ray")
current = ""
refs = []
class Remote:
 def __init__(self, fn, options): self.fn, self.opts = fn, options
 def options(self, **kw): return Remote(self.fn, dict(self.opts, **kw))
 def remote(self, *args):
  ref = types.SimpleNamespace(fn=self.fn, opts=self.opts, args=args)
  # A hashable reference, as provided by Ray.
  key = len(refs); refs.append(ref); return key
ray.remote = lambda **kw: lambda fn: Remote(fn, kw)
ray.init = lambda **kw: None
ray.shutdown = lambda: None
ray.cancel = lambda *a, **kw: None
ray.nodes = lambda: [
 {"Alive":True,"NodeID":"cpu","NodeManagerAddress":"cpu","Resources":{"CPU":4}},
 *[{"Alive":True,"NodeID":v,"NodeManagerAddress":v,"Resources":{"CPU":4,"GPU":2,"plainshow_gpu_"+v:2}} for v in ("nvidia","amd","intel")],
 {"Alive":False,"NodeID":"off","NodeManagerAddress":"off","Resources":{"GPU":1}},
]
ray.get_runtime_context = lambda: types.SimpleNamespace(get_node_id=lambda:current)
def get(ref):
 global current
 task = refs[ref]; current = task.opts["scheduling_strategy"].node_id
 if task.fn.__name__ == "gpu_sample":
  assert task.opts["num_gpus"] == 1
  return {"name":"GPU", "status":"missing", "detail":"Missing test framework"}
 return task.fn(*task.args)
ray.get = get
ray.wait = lambda refs, **kw: ([], refs) if config["busy"] and refs and globals()["refs"][refs[0]].fn.__name__ == "gpu_sample" else (refs, [])
strategy = types.ModuleType("ray.util.scheduling_strategies")
class Affinity:
 def __init__(self, node_id, soft):
  assert soft is False
  self.node_id = node_id
strategy.NodeAffinitySchedulingStrategy = Affinity
sys.modules.update({"ray":ray,"ray.util":types.ModuleType("ray.util"),"ray.util.scheduling_strategies":strategy})
ns = {"__name__":"diagnostic_test"}
exec(compile(config["code"],"diagnostic.py","exec"), ns)
ns["software"] = lambda resources: [{"name":"CUDA toolkit","status":"missing","detail":"No toolkit"}]
output = io.StringIO()
with contextlib.redirect_stdout(output): ns["main"]()
rows = json.loads(output.getvalue().split("PLAINSHOW_TEST=")[1])
assert len(rows) == 4 and all(row["ok"] for row in rows), rows
assert all(row["cpu"]["status"] == "passed" for row in rows), rows
assert rows[0]["gpu"]["status"] == "skipped", rows
assert all(row["gpu"]["status"] == ("skipped" if config["busy"] else "missing") for row in rows[1:]), rows

# Execute the actual isolated calculation code for CUDA, ROCm and Intel XPU.
for vendor in ("nvidia", "amd", "intel"):
 torch = types.ModuleType("torch")
 torch.__version__ = "test"
 torch.version = types.SimpleNamespace(hip="6.0" if vendor == "amd" else None, cuda="12.8")
 backend = types.SimpleNamespace(is_available=lambda:True, get_device_name=lambda i:"test GPU")
 torch.cuda = backend; torch.xpu = backend
 class Tensor:
  def __matmul__(self, other): return self
  def cpu(self): return self
  def tolist(self): return [[7.,10.],[15.,22.]]
 def tensor(data, device):
  assert device == ("xpu:0" if vendor == "intel" else "cuda:0")
  return Tensor()
 torch.tensor = tensor
 sys.modules["torch"] = torch
 sys.argv = ["gpu", vendor]
 output = io.StringIO()
 with contextlib.redirect_stdout(output): exec(ns["GPU_CODE"], {})
 assert json.loads(output.getvalue().split("PLAINSHOW_GPU=")[1])["status"] == "passed", output.getvalue()
 backend.is_available = lambda:False
 output = io.StringIO()
 with contextlib.redirect_stdout(output): exec(ns["GPU_CODE"], {})
 assert json.loads(output.getvalue().split("PLAINSHOW_GPU=")[1])["status"] == "warning", output.getvalue()

torch.version.hip = None
sys.argv = ["gpu", "amd"]
output = io.StringIO()
with contextlib.redirect_stdout(output):
 try: exec(ns["GPU_CODE"], {})
 except SystemExit: pass
assert json.loads(output.getvalue().split("PLAINSHOW_GPU=")[1])["status"] == "missing", output.getvalue()
sys.modules["torch"] = None
output = io.StringIO()
with contextlib.redirect_stdout(output):
 try: exec(ns["GPU_CODE"], {})
 except SystemExit: pass
assert json.loads(output.getvalue().split("PLAINSHOW_GPU=")[1])["status"] == "missing", output.getvalue()
`
	for _, busy := range []bool{false, true} {
		raw, _ := json.Marshal(map[string]any{"code": diagnosticCode, "busy": busy})
		cmd := exec.Command(python, "-c", harness)
		cmd.Stdin = strings.NewReader(string(raw))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("busy=%v: %v\n%s", busy, err, output)
		}
	}
}
