"""Plainshow Cluster: __TITLE__.

Run from your project folder:
  ray job submit --address __DASHBOARD__ --working-dir . -- python __FILENAME__
Or use the branch editor's Run button to use the currently active network.
For an external IDE, set RAY_ADDRESS to the desired network's Ray head.
Install your own framework/data dependencies on every participating worker.
This distributes independent tasks; it is NOT cross-vendor synchronous DDP.
"""
import os
import ray
from ray.util.scheduling_strategies import NodeAffinitySchedulingStrategy

MODE = "__MODE__"
# Optional cap per device; 0 uses every CPU/GPU that Ray advertises under policy.
MAX_WORKERS_PER_DEVICE = __LIMIT__
TIMEOUT_SECONDS = 600


@ray.remote(max_retries=0)
def work(shard, backend):
    # PUT YOUR CODE HERE: load this shard, train a model, or run inference.
    # CPU: ordinary Python, NumPy, or a CPU framework build.
    # NVIDIA: a CUDA framework; AMD: a supported ROCm build.
    # Intel GPU: an XPU-capable framework and supported driver.
    # Import frameworks HERE so CPU-only machines need no GPU framework.
    # Ray assigns visible devices; use device 0 in your framework, not shard.
    # Return a small result/checkpoint path; do not return a whole dataset.
    return {"shard": shard, "backend": backend, "node": ray.get_runtime_context().get_node_id()}


def main():
    # The default is the active head when this file was created. The branch
    # editor submits to the current active network; Ray Jobs supplies RAY_ADDRESS.
    address = os.environ.get("RAY_ADDRESS", "__HEAD__").strip()
    if not address:
        raise RuntimeError("Select a network and start Ray, then set RAY_ADDRESS to its head or run this file through the Plainshow branch editor.")
    ray.init(address=address)
    pending = []
    try:
        for node in sorted((n for n in ray.nodes() if n["Alive"]), key=lambda n: n["NodeID"]):
            resources = node.get("Resources", {})
            cpus = int(resources.get("CPU", 0))
            gpus = int(resources.get("GPU", 0))
            vendors = [v for v in ("nvidia", "amd", "intel") if resources.get("plainshow_gpu_" + v, 0) > 0]
            # Nodes with multiple GPU vendors need an explicit framework/device
            # strategy; a vendor resource alone cannot select a physical GPU.
            vendor = vendors[0] if len(vendors) == 1 else "unknown"
            use_gpu = MODE != "cpu" and gpus > 0
            if MODE in ("nvidia", "amd", "intel") and vendor != MODE:
                continue
            if MODE == "gpu" and not use_gpu:
                continue
            if use_gpu and vendor == "unknown":
                print("Skipping ambiguous GPU device:", node["NodeManagerAddress"])
                continue
            if not use_gpu and MODE not in ("cpu", "mixed"):
                continue
            slots = gpus if use_gpu else cpus
            if MAX_WORKERS_PER_DEVICE:
                slots = min(slots, MAX_WORKERS_PER_DEVICE)
            for _ in range(slots):
                # Fractional CPU reservations let all GPUs participate even
                # when a device contributes fewer CPU cores than GPUs.
                options = {"num_cpus": min(1, cpus / gpus) if use_gpu else 1, "num_gpus": 1 if use_gpu else 0,
                           "scheduling_strategy": NodeAffinitySchedulingStrategy(node["NodeID"], soft=False)}
                if use_gpu:
                    options["resources"] = {"plainshow_gpu_" + vendor: 1}
                pending.append(work.options(**options).remote(len(pending), vendor if use_gpu else "cpu"))
        if not pending:
            raise RuntimeError("No compatible workers. Check Test, compute assignments and device policies.")
        print("Workers:", len(pending))
        # PUT YOUR AGGREGATION / CHECKPOINT HANDLING HERE.
        for result in ray.get(pending, timeout=TIMEOUT_SECONDS):
            print(result)
    finally:
        for task in pending:
            ray.cancel(task, force=True)
        ray.shutdown()


if __name__ == "__main__":
    main()
