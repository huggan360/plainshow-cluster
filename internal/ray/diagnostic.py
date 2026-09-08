"""Read-only software inventory and bounded CPU/GPU execution checks."""
import importlib.metadata
import json
import os
import shutil
import socket
import subprocess
import sys
import ray
from ray.util.scheduling_strategies import NodeAffinitySchedulingStrategy


def probe(name, status, detail):
    return {"name": name, "status": status, "detail": detail}


def software(resources):
    checks = []
    commands = [
        ("nvidia", "NVIDIA driver", "nvidia-smi", ["--query-gpu=name,driver_version", "--format=csv,noheader"]),
        ("nvidia", "CUDA toolkit", "nvcc", ["--version"]),
        ("amd", "ROCm/HIP toolkit", "hipconfig", ["--version"]),
        ("amd", "ROCm devices", "rocminfo", []),
        ("intel", "Intel oneAPI/SYCL devices", "sycl-ls", []),
        ("intel", "OpenCL devices", "clinfo", ["-l"]),
    ]
    for vendor, name, command, args in commands:
        executable = shutil.which(command)
        if not executable and vendor == "amd" and os.path.isfile("/opt/rocm/bin/" + command):
            executable = "/opt/rocm/bin/" + command
        if not executable:
            if resources.get("plainshow_gpu_" + vendor, 0) > 0:
                checks.append(probe(name, "missing", command + " not found in the Ray environment; this alone does not prove its runtime is missing."))
            continue
        try:
            result = subprocess.run([executable] + args, capture_output=True, text=True, timeout=2)
            detail = " ".join((result.stdout or result.stderr).split())[:450]
            checks.append(probe(name, "available" if result.returncode == 0 else "warning",
                                detail or "Command exit code " + str(result.returncode)))
        except Exception as error:
            checks.append(probe(name, "warning", str(error)[:450]))
    try:
        checks.append(probe("PyTorch", "available", importlib.metadata.version("torch") + " in " + sys.executable))
    except importlib.metadata.PackageNotFoundError:
        checks.append(probe("PyTorch", "missing", "Not installed in " + sys.executable + "; GPU execution cannot be verified by this test."))
    return checks


# Isolate native framework imports/kernels from reachability. Keep Ray's assigned
# GPU visibility untouched; never run on a device outside the task allocation.
GPU_CODE = r'''
import json, sys
def report(status, detail):
    print("PLAINSHOW_GPU=" + json.dumps({"name":"GPU calculation", "status":status, "detail":detail}), flush=True)
try:
    import torch
except ModuleNotFoundError:
    report("missing", "PyTorch is not installed in this Ray environment.")
    sys.exit(0)
except Exception as error:
    report("warning", "PyTorch import failed: " + str(error)[:400])
    sys.exit(0)
try:
    vendor = sys.argv[1]
    if vendor == "intel":
        backend = getattr(torch, "xpu", None)
        device, runtime = "xpu:0", "Intel XPU"
    else:
        backend = torch.cuda
        device = "cuda:0"
        hip = getattr(torch.version, "hip", None)
        runtime = "ROCm/HIP " + str(hip) if hip else "CUDA " + str(torch.version.cuda)
        if vendor == "amd" and not hip:
            report("missing", "This PyTorch build has no ROCm/HIP support for the AMD GPU.")
            sys.exit(0)
        if vendor == "nvidia" and hip:
            report("missing", "This PyTorch build targets ROCm, not the NVIDIA GPU.")
            sys.exit(0)
    if backend is None or not backend.is_available():
        report("warning", runtime + " is not available to PyTorch. Check its build, driver and Ray device visibility.")
    else:
        a = torch.tensor([[1., 2.], [3., 4.]], device=device)
        if (a @ a).cpu().tolist() != [[7., 10.], [15., 22.]]:
            raise RuntimeError("GPU calculation returned an incorrect result")
        report("passed", backend.get_device_name(0) + " · " + runtime + " · PyTorch " + torch.__version__ + " · matrix calculation passed")
except Exception as error:
    report("warning", str(error)[:450])
'''


@ray.remote(num_cpus=0, max_retries=0)
def ping(resources):
    cpu_ok = sum(i * i for i in range(1000)) == 332833500
    row = {"hostname": socket.gethostname(), "node_id": ray.get_runtime_context().get_node_id(),
           "cpu": probe("CPU calculation", "passed" if cpu_ok else "warning",
                        "Python arithmetic passed" if cpu_ok else "Unexpected arithmetic result")}
    try:
        row["software"] = software(resources)
    except Exception as error:
        row["software"] = [probe("Software inventory", "warning", str(error)[:450])]
    return row


@ray.remote(num_cpus=0, num_gpus=1, max_retries=0)
def gpu_sample(vendor):
    try:
        # An external deadline survives cancellation of this Ray worker, so a
        # stuck native driver cannot leave an unbounded child process behind.
        limiter = shutil.which("timeout")
        if not limiter:
            return probe("GPU calculation", "skipped", "The timeout utility is missing; cannot safely bound the native GPU check.")
        result = subprocess.run([limiter, "-s", "KILL", "12", sys.executable, "-c", GPU_CODE, vendor],
                                capture_output=True, text=True, timeout=15)
        for line in result.stdout.splitlines():
            if line.startswith("PLAINSHOW_GPU="):
                check = json.loads(line[len("PLAINSHOW_GPU="):])
                ids = ray.get_runtime_context().get_accelerator_ids().get("GPU", [])
                check["detail"] += " · Ray GPU IDs: " + ", ".join(map(str, ids))
                return check
        if result.returncode in (124, 137, -9):
            return probe("GPU calculation", "warning", "Framework import or GPU calculation exceeded 12 seconds.")
        return probe("GPU calculation", "warning", "Framework process failed (exit " + str(result.returncode) + "): " + result.stderr[-450:])
    except subprocess.TimeoutExpired:
        return probe("GPU calculation", "warning", "Framework import or GPU calculation exceeded 12 seconds.")
    except Exception as error:
        return probe("GPU calculation", "warning", str(error)[:450])


def main():
    ray.init(address="auto")
    pending, gpu_pending = {}, {}
    try:
        for node in (n for n in ray.nodes() if n["Alive"]):
            affinity = NodeAffinitySchedulingStrategy(node["NodeID"], soft=False)
            pending[ping.options(scheduling_strategy=affinity).remote(node.get("Resources", {}))] = node
        _, remaining = ray.wait(list(pending), num_returns=len(pending), timeout=25) if pending else ([], [])
        results = []
        for ref, node in pending.items():
            resources = node.get("Resources", {})
            row = {"address": node["NodeManagerAddress"], "resources": resources, "ok": False}
            try:
                if ref in remaining:
                    raise TimeoutError("Worker did not answer within 25 seconds")
                row.update(ray.get(ref))
                row["ok"] = row["node_id"] == node["NodeID"]
            except Exception as error:
                row["error"] = str(error)
            row["gpu"] = probe("GPU calculation", "skipped", "No whole GPU assigned to Ray; CPU-only operation is supported.")
            if row["ok"] and resources.get("GPU", 0) >= 1:
                vendors = [v for v in ("nvidia", "amd", "intel") if resources.get("plainshow_gpu_" + v, 0) > 0]
                if len(vendors) != 1:
                    row["gpu"] = probe("GPU calculation", "skipped", "GPU vendor is unknown or mixed on this device; choose a vendor-specific setup to test safely.")
                else:
                    affinity = NodeAffinitySchedulingStrategy(node["NodeID"], soft=False)
                    gpu_pending[gpu_sample.options(scheduling_strategy=affinity).remote(vendors[0])] = row
            results.append(row)
        _, remaining_gpu = ray.wait(list(gpu_pending), num_returns=len(gpu_pending), timeout=20) if gpu_pending else ([], [])
        for ref, row in gpu_pending.items():
            try:
                if ref in remaining_gpu:
                    row["gpu"] = probe("GPU calculation", "skipped", "No result within 20 seconds. GPU may be busy, restricted or initializing; connectivity still passed.")
                else:
                    row["gpu"] = ray.get(ref)
            except Exception as error:
                row["gpu"] = probe("GPU calculation", "warning", str(error)[:450])
        print("PLAINSHOW_TEST=" + json.dumps(results), flush=True)
    finally:
        for ref in list(pending) + list(gpu_pending):
            ray.cancel(ref, force=True)
        ray.shutdown()


if __name__ == "__main__":
    main()
