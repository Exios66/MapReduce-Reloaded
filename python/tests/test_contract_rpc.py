"""Check generated RPC bindings without a server or network connection."""

from unittest.mock import Mock

import grpc
import pytest

from mapreduce_reloaded.gen.mapreduce.v1 import (
    coordinator_pb2 as coordinator,
)
from mapreduce_reloaded.gen.mapreduce.v1 import (
    coordinator_pb2_grpc,
    shuffle_pb2_grpc,
)
from mapreduce_reloaded.gen.mapreduce.v1 import (
    shuffle_pb2 as shuffle,
)
from mapreduce_reloaded.gen.mapreduce.v1 import (
    types_pb2 as types,
)

COORDINATOR_RPCS = [
    (
        "RegisterWorker",
        coordinator.RegisterWorkerRequest(
            worker=types.WorkerInfo(
                shuffle_address="worker:7070", host="worker", zone="rack-a", slots=4
            )
        ),
        coordinator.RegisterWorkerResponse(worker_id="w1", heartbeat_interval={"seconds": 2}),
    ),
    (
        "RequestTask",
        coordinator.RequestTaskRequest(worker_id="w1"),
        coordinator.RequestTaskResponse(shutdown={}),
    ),
    (
        "ReportTask",
        coordinator.ReportTaskRequest(worker_id="w1", success={}),
        coordinator.ReportTaskResponse(accepted=True),
    ),
    (
        "Heartbeat",
        coordinator.HeartbeatRequest(worker_id="w1"),
        coordinator.HeartbeatResponse(reregister=True),
    ),
    (
        "SubmitJob",
        coordinator.SubmitJobRequest(job=types.JobSpec(program="wordcount")),
        coordinator.SubmitJobResponse(job_id="job-1"),
    ),
    (
        "GetJobStatus",
        coordinator.GetJobStatusRequest(job_id="job-1"),
        coordinator.GetJobStatusResponse(job_id="job-1", state=coordinator.JOB_STATE_SUCCEEDED),
    ),
]
SHUFFLE_RPC = (
    "FetchPartition",
    shuffle.FetchPartitionRequest(job_id="job-1", map_index=7, partition=3, offset=2**53 + 1),
    shuffle.FetchPartitionResponse(data=b"\x00\xffchunk"),
)


@pytest.mark.parametrize(
    ("stub_type", "service", "binding", "rpc"),
    [
        (coordinator_pb2_grpc.CoordinatorServiceStub, "CoordinatorService", "unary_unary", rpc)
        for rpc in COORDINATOR_RPCS
    ]
    + [(shuffle_pb2_grpc.ShuffleServiceStub, "ShuffleService", "unary_stream", SHUFFLE_RPC)],
    ids=[rpc[0] for rpc in COORDINATOR_RPCS] + ["FetchPartition"],
)
def test_client_bindings_use_contract_paths_and_codecs(stub_type, service, binding, rpc):
    name, request, response = rpc
    channel = Mock(spec=grpc.Channel)
    bound_methods = {}

    def bind(path, **codecs):
        call = Mock(name=path)
        bound_methods[path] = (call, codecs)
        return call

    getattr(channel, binding).side_effect = bind
    stub = stub_type(channel)
    call, codecs = bound_methods[f"/mapreduce.v1.{service}/{name}"]
    assert codecs["request_serializer"](request) == request.SerializeToString()
    assert codecs["response_deserializer"](response.SerializeToString()) == response
    assert getattr(stub, name) is call
    other_binding = "unary_stream" if binding == "unary_unary" else "unary_unary"
    getattr(channel, other_binding).assert_not_called()


@pytest.mark.parametrize(
    ("module", "service", "rpcs", "streaming"),
    [
        (coordinator_pb2_grpc, "CoordinatorService", COORDINATOR_RPCS, False),
        (shuffle_pb2_grpc, "ShuffleService", [SHUFFLE_RPC], True),
    ],
)
def test_server_registration_uses_contract_codecs_and_unimplemented_status(
    module, service, rpcs, streaming
):
    server = Mock(spec=grpc.Server)
    servicer = getattr(module, f"{service}Servicer")()
    getattr(module, f"add_{service}Servicer_to_server")(servicer, server)
    server.add_generic_rpc_handlers.assert_called_once()
    server.add_registered_method_handlers.assert_called_once()
    service_name, handlers = server.add_registered_method_handlers.call_args.args
    assert service_name == f"mapreduce.v1.{service}"
    assert set(handlers) == {rpc[0] for rpc in rpcs}
    for name, request, response in rpcs:
        handler = handlers[name]
        assert not handler.request_streaming
        assert handler.response_streaming is streaming
        assert handler.request_deserializer(request.SerializeToString()) == request
        assert handler.response_serializer(response) == response.SerializeToString()
        context = Mock(spec=grpc.ServicerContext)
        behavior = handler.unary_stream if streaming else handler.unary_unary
        with pytest.raises(NotImplementedError):
            behavior(request, context)
        context.set_code.assert_called_once_with(grpc.StatusCode.UNIMPLEMENTED)
