"""Checks the generated Python code against the shared contract in proto/.

Mirrors go/internal/contract/contract_test.go, so both languages are held to
the same golden bytes.
"""

from concurrent import futures
from pathlib import Path

import grpc
import pytest
from google.protobuf import text_format

from mapreduce_reloaded.gen.mapreduce.v1 import (
    coordinator_pb2,
    coordinator_pb2_grpc,
    types_pb2,
)

GOLDEN = Path(__file__).resolve().parents[2] / "testdata" / "proto"


def test_golden_map_task():
    task = text_format.Parse((GOLDEN / "map_task.txtpb").read_text(), coordinator_pb2.MapTask())
    want = (GOLDEN / "map_task.binpb").read_bytes()

    assert task.SerializeToString(deterministic=True) == want
    assert coordinator_pb2.MapTask.FromString(want) == task


class FakeCoordinator(coordinator_pb2_grpc.CoordinatorServiceServicer):
    """Hands out a single map task, then tells workers to wait."""

    def __init__(self):
        self.handed_out = False

    def RequestTask(self, request, context):  # noqa: N802 (gRPC method name)
        if not self.handed_out:
            self.handed_out = True
            return coordinator_pb2.RequestTaskResponse(
                map_task=coordinator_pb2.MapTask(
                    attempt=types_pb2.TaskAttemptId(
                        task=types_pb2.TaskId(job_id="job-1", kind=types_pb2.TASK_KIND_MAP)
                    ),
                    split=types_pb2.InputSplit(uri="file:///in.txt", length=100),
                )
            )
        response = coordinator_pb2.RequestTaskResponse()
        response.wait.retry_after.FromMilliseconds(500)
        return response


@pytest.fixture
def coordinator_stub():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    coordinator_pb2_grpc.add_CoordinatorServiceServicer_to_server(FakeCoordinator(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    channel = grpc.insecure_channel(f"127.0.0.1:{port}")
    yield coordinator_pb2_grpc.CoordinatorServiceStub(channel)
    channel.close()
    server.stop(grace=None)


def test_coordinator_service_round_trip(coordinator_stub):
    first = coordinator_stub.RequestTask(coordinator_pb2.RequestTaskRequest(worker_id="w1"))
    assert first.WhichOneof("assignment") == "map_task"
    assert first.map_task.split.uri == "file:///in.txt"

    second = coordinator_stub.RequestTask(coordinator_pb2.RequestTaskRequest(worker_id="w1"))
    assert second.WhichOneof("assignment") == "wait"

    with pytest.raises(grpc.RpcError) as err:
        coordinator_stub.Heartbeat(coordinator_pb2.HeartbeatRequest(worker_id="w1"))
    assert err.value.code() == grpc.StatusCode.UNIMPLEMENTED
