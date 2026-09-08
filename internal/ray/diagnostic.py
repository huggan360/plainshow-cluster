import json
import socket
import ray
from ray.util.scheduling_strategies import NodeAffinitySchedulingStrategy

ray.init(address="auto")

@ray.remote(num_cpus=0, max_retries=0)
def ping():
    return {"hostname": socket.gethostname(), "node_id": ray.get_runtime_context().get_node_id()}

nodes = [n for n in ray.nodes() if n["Alive"]]
pending = {ping.options(scheduling_strategy=NodeAffinitySchedulingStrategy(n["NodeID"], soft=False)).remote(): n for n in nodes}
ready, remaining = ray.wait(list(pending), num_returns=len(pending), timeout=25) if pending else ([], [])
results = []
for ref, node in pending.items():
    row = {"address": node["NodeManagerAddress"], "resources": node.get("Resources", {}), "ok": False}
    try:
        if ref in remaining:
            ray.cancel(ref, force=True)
            raise TimeoutError("Worker did not answer within 25 seconds")
        row.update(ray.get(ref))
        row["ok"] = row["node_id"] == node["NodeID"]
    except Exception as error:
        row["error"] = str(error)
    results.append(row)
print("PLAINSHOW_TEST=" + json.dumps(results), flush=True)
ray.shutdown()
