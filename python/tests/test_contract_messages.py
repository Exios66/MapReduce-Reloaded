"""Unit checks for the wire contract introduced in ADR 0003.

The small wire vectors are also checked in Go's messages_test.go. Keep them
literal: deriving the expected bytes with protobuf would hide schema changes.
"""

import pytest
from google.protobuf.message import DecodeError

from mapreduce_reloaded.gen.mapreduce.v1 import (
    coordinator_pb2 as coordinator,
)
from mapreduce_reloaded.gen.mapreduce.v1 import (
    shuffle_pb2 as shuffle,
)
from mapreduce_reloaded.gen.mapreduce.v1 import (
    types_pb2 as types,
)


@pytest.mark.parametrize(
    ("message", "wire_hex"),
    [
        pytest.param(coordinator.RequestTaskResponse(), "", id="no-assignment"),
        pytest.param(coordinator.RequestTaskResponse(map_task={}), "0a00", id="empty-map"),
        pytest.param(coordinator.RequestTaskResponse(reduce_task={}), "1200", id="empty-reduce"),
        pytest.param(coordinator.RequestTaskResponse(wait={}), "1a00", id="empty-wait"),
        pytest.param(coordinator.RequestTaskResponse(shutdown={}), "2200", id="shutdown"),
        pytest.param(coordinator.TaskFailure(), "", id="unknown-failed-record"),
        pytest.param(coordinator.TaskFailure(failed_record=0), "1000", id="failed-record-zero"),
        pytest.param(
            coordinator.TaskFailure(failed_record=2**64 - 1),
            "10ffffffffffffffffff01",
            id="failed-record-max",
        ),
        pytest.param(
            shuffle.FetchPartitionRequest(offset=2**64 - 1),
            "20ffffffffffffffffff01",
            id="resume-offset-max",
        ),
        pytest.param(types.TaskId(index=2**32 - 1), "18ffffffff0f", id="task-index-max"),
        pytest.param(
            shuffle.FetchPartitionResponse(data=b"\x00\xff\x80\n"),
            "0a0400ff800a",
            id="binary-shuffle-chunk",
        ),
    ],
)
def test_shared_wire_vectors(message, wire_hex):
    wire = bytes.fromhex(wire_hex)
    assert message.SerializeToString(deterministic=True) == wire
    assert type(message).FromString(wire) == message


@pytest.mark.parametrize("field", ["map_task", "reduce_task", "wait", "shutdown"])
def test_assignment_presence_and_replacement(field):
    response = coordinator.RequestTaskResponse()
    assert response.WhichOneof("assignment") is None

    getattr(response, field).SetInParent()
    decoded = coordinator.RequestTaskResponse.FromString(response.SerializeToString())
    assert decoded.WhichOneof("assignment") == field

    replacement = "map_task" if field == "shutdown" else "shutdown"
    getattr(decoded, replacement).SetInParent()
    assert not decoded.HasField(field)
    assert decoded.WhichOneof("assignment") == replacement
    decoded.ClearField("assignment")
    assert decoded.WhichOneof("assignment") is None


def test_report_outcomes_replace_each_other_without_losing_attempt_identity():
    report = coordinator.ReportTaskRequest(
        worker_id="worker-1",
        attempt=types.TaskAttemptId(
            task=types.TaskId(job_id="job-1", kind=types.TASK_KIND_MAP, index=7), attempt=2
        ),
        success=coordinator.TaskSuccess(counters=types.Counters(values={"records": 5})),
    )
    attempt = report.attempt.SerializeToString()
    report.failure.CopyFrom(coordinator.TaskFailure(message="bad record", failed_record=0))
    decoded = coordinator.ReportTaskRequest.FromString(report.SerializeToString())
    assert decoded.WhichOneof("outcome") == "failure"
    assert not decoded.HasField("success")
    assert decoded.failure.HasField("failed_record")
    assert decoded.failure.failed_record == 0
    assert decoded.attempt.SerializeToString() == attempt
    assert decoded.worker_id == "worker-1"

    decoded.success.SetInParent()
    assert decoded.WhichOneof("outcome") == "success"
    assert not decoded.HasField("failure")


def test_failed_record_absent_and_explicit_zero_remain_distinct():
    unknown = coordinator.TaskFailure()
    first_record = coordinator.TaskFailure(failed_record=0)
    assert unknown.failed_record == first_record.failed_record == 0
    assert not unknown.HasField("failed_record")
    assert first_record.HasField("failed_record")
    assert unknown != first_record
    first_record.ClearField("failed_record")
    assert first_record == unknown


def test_reduce_task_preserves_attempt_sources_and_large_record_numbers():
    task = coordinator.ReduceTask(
        attempt=types.TaskAttemptId(
            task=types.TaskId(job_id="job-1", kind=types.TASK_KIND_REDUCE, index=3), attempt=4
        ),
        job=types.JobSpec(
            program="grep",
            inputs=["file:///a", "file:///b"],
            output="s3://bucket/out",
            num_reduce_tasks=4,
            input_format="text",
            output_format="kv",
            partitioner="custom",
            use_combiner=True,
            params={"pattern": "λ", "mode": "exact"},
        ),
        sources=[
            types.PartitionLocation(
                map_index=0, partition=3, worker_address="worker:7070", size_bytes=2**53 + 1
            ),
            types.PartitionLocation(map_index=1, partition=3, uri="s3://bucket/part", size_bytes=0),
        ],
        skip_records=[0, 2**53 + 1, 2**64 - 1],
    )
    decoded = coordinator.ReduceTask.FromString(task.SerializeToString(deterministic=True))
    assert decoded == task
    assert [source.map_index for source in decoded.sources] == [0, 1]
    assert decoded.sources[0].size_bytes == 2**53 + 1
    assert decoded.sources[1].worker_address == ""
    assert decoded.job.split_size_bytes == 0  # The default split size is applied by consumers.


@pytest.mark.parametrize("fraction", [0.0, 0.5, 1.0])
def test_heartbeat_preserves_progress_cancellation_and_reregistration(fraction):
    attempt = types.TaskAttemptId(
        task=types.TaskId(job_id="job-1", kind=types.TASK_KIND_MAP), attempt=1
    )
    request = coordinator.HeartbeatRequest(
        worker_id="worker-1",
        running=[coordinator.TaskProgress(attempt=attempt, fraction_done=fraction)],
    )
    assert coordinator.HeartbeatRequest.FromString(request.SerializeToString()) == request
    response = coordinator.HeartbeatResponse(cancel=[attempt], reregister=True)
    assert coordinator.HeartbeatResponse.FromString(response.SerializeToString()) == response


def test_job_status_preserves_failure_and_signed_counter_limits():
    status = coordinator.GetJobStatusResponse(
        job_id="job-1",
        state=coordinator.JOB_STATE_FAILED,
        map_tasks_total=10,
        map_tasks_completed=9,
        reduce_tasks_total=4,
        reduce_tasks_completed=2,
        counters=types.Counters(values={"min": -(2**63), "max": 2**63 - 1, "empty": 0}),
        error="too many bad records",
    )
    assert coordinator.GetJobStatusResponse.FromString(status.SerializeToString()) == status


def test_map_serialization_is_independent_of_insertion_order():
    first = types.JobSpec(params={"z": "last", "a": "first"})
    second = types.JobSpec(params={"a": "first", "z": "last"})
    assert first.SerializeToString(deterministic=True) == second.SerializeToString(
        deterministic=True
    )
    first_counters = types.Counters(values={"z": -1, "a": 2})
    second_counters = types.Counters(values={"a": 2, "z": -1})
    assert first_counters.SerializeToString(
        deterministic=True
    ) == second_counters.SerializeToString(deterministic=True)


@pytest.mark.parametrize(
    ("message_type", "field", "bits"),
    [
        (types.TaskId, "index", 32),
        (types.TaskAttemptId, "attempt", 32),
        (types.InputSplit, "offset", 64),
        (types.InputSplit, "length", 64),
        (types.PartitionLocation, "size_bytes", 64),
        (coordinator.TaskFailure, "failed_record", 64),
        (shuffle.FetchPartitionRequest, "offset", 64),
    ],
)
@pytest.mark.parametrize("invalid", ["negative", "overflow"])
def test_unsigned_fields_reject_out_of_range_values(message_type, field, bits, invalid):
    with pytest.raises(ValueError):
        message_type(**{field: -1 if invalid == "negative" else 2**bits})


@pytest.mark.parametrize(
    "message",
    [
        types.TaskId(job_id="job"),
        coordinator.HeartbeatResponse(reregister=True),
        shuffle.FetchPartitionRequest(job_id="job", offset=17),
    ],
)
def test_unknown_fields_survive_forwarding(message):
    # A future field 100 containing varint 7 must survive an older worker.
    unknown = bytes.fromhex("a00607")
    decoded = type(message).FromString(message.SerializeToString() + unknown)
    forwarded = decoded.SerializeToString()
    assert forwarded.endswith(unknown)
    decoded.DiscardUnknownFields()
    assert decoded == message


@pytest.mark.parametrize(
    ("message_type", "field"),
    [(types.TaskId, "kind"), (coordinator.GetJobStatusResponse, "state")],
)
def test_future_enum_values_survive_round_trip(message_type, field):
    message = message_type(**{field: 99})
    assert getattr(message_type.FromString(message.SerializeToString()), field) == 99


@pytest.mark.parametrize("wire", [b"\x00", b"\x0a\x05ab", b"\x80"])
@pytest.mark.parametrize(
    "message_type", [coordinator.RequestTaskResponse, shuffle.FetchPartitionResponse]
)
def test_malformed_wire_data_is_rejected(message_type, wire):
    with pytest.raises(DecodeError):
        message_type.FromString(wire)
