package ray

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// Exercise the actual generated Python against a small Ray API double. The
// important contract is placement on heterogeneous live hardware, not wording.
func TestPresetsPlaceTasksOnLiveCompatibleHardware(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python unavailable")
	}
	const harness = `
import json, sys, types
config=json.load(sys.stdin)
calls=[]
ray=types.ModuleType("ray")
ray.init=lambda **kw: None
ray.shutdown=lambda: None
ray.cancel=lambda *a, **kw: None
ray.get=lambda values, **kw: values
ray.nodes=lambda: [
 {"Alive":True,"NodeID":"nv","NodeManagerAddress":"nv","Resources":{"CPU":1,"GPU":3,"plainshow_gpu_nvidia":3}},
 {"Alive":True,"NodeID":"amd","NodeManagerAddress":"amd","Resources":{"CPU":8,"GPU":2,"plainshow_gpu_amd":2}},
 {"Alive":True,"NodeID":"cpu","NodeManagerAddress":"cpu","Resources":{"CPU":4}},
 {"Alive":False,"NodeID":"off","NodeManagerAddress":"off","Resources":{"CPU":99,"GPU":99,"plainshow_gpu_nvidia":99}},
]
class Remote:
 def options(self, **kw):
  self.kw=kw
  return self
 def remote(self,*args):
  row=(self.kw["scheduling_strategy"].node_id,args[1],self.kw["num_gpus"])
  calls.append(row)
  return row
ray.remote=lambda **kw: lambda fn: Remote()
strategy=types.ModuleType("ray.util.scheduling_strategies")
class Affinity:
 def __init__(self,node_id,soft):
  assert soft is False
  self.node_id=node_id
strategy.NodeAffinitySchedulingStrategy=Affinity
sys.modules["ray"]=ray
sys.modules["ray.util"]=types.ModuleType("ray.util")
sys.modules["ray.util.scheduling_strategies"]=strategy
try:
 exec(compile(config["code"],"preset.py","exec"),{"__name__":"__main__"})
except RuntimeError:
 if config["count"]!=0: raise
assert len(calls)==config["count"],calls
assert all(row[0]!="off" for row in calls),calls
if config["mode"]=="cpu": assert all(row[1]=="cpu" and row[2]==0 for row in calls),calls
if config["mode"] in ("nvidia","amd"): assert all(row[1]==config["mode"] for row in calls),calls
if config["mode"]=="mixed": assert sum(row[1]=="cpu" for row in calls)==4,calls
`
	for mode, count := range map[string]int{"cpu": 13, "gpu": 5, "mixed": 9, "nvidia": 3, "amd": 2, "intel": 0} {
		t.Run(mode, func(t *testing.T) {
			code, err := Preset(mode, "p", "ray_job.py", 0)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(map[string]any{"code": code, "count": count, "mode": mode})
			cmd := exec.Command(python, "-c", harness)
			cmd.Stdin = strings.NewReader(string(raw))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}
