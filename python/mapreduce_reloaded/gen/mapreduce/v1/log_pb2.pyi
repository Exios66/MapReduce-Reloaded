import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2 as _coordinator_pb2
from mapreduce_reloaded.gen.mapreduce.v1 import types_pb2 as _types_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class LogRecord(_message.Message):
    __slots__ = ("job_submitted", "task_assigned", "task_reported", "worker_lost", "restarted", "at")
    JOB_SUBMITTED_FIELD_NUMBER: _ClassVar[int]
    TASK_ASSIGNED_FIELD_NUMBER: _ClassVar[int]
    TASK_REPORTED_FIELD_NUMBER: _ClassVar[int]
    WORKER_LOST_FIELD_NUMBER: _ClassVar[int]
    RESTARTED_FIELD_NUMBER: _ClassVar[int]
    AT_FIELD_NUMBER: _ClassVar[int]
    job_submitted: JobSubmitted
    task_assigned: TaskAssigned
    task_reported: _coordinator_pb2.ReportTaskRequest
    worker_lost: WorkerLost
    restarted: Restarted
    at: _timestamp_pb2.Timestamp
    def __init__(self, job_submitted: _Optional[_Union[JobSubmitted, _Mapping]] = ..., task_assigned: _Optional[_Union[TaskAssigned, _Mapping]] = ..., task_reported: _Optional[_Union[_coordinator_pb2.ReportTaskRequest, _Mapping]] = ..., worker_lost: _Optional[_Union[WorkerLost, _Mapping]] = ..., restarted: _Optional[_Union[Restarted, _Mapping]] = ..., at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class JobSubmitted(_message.Message):
    __slots__ = ("job_id", "job", "splits")
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    JOB_FIELD_NUMBER: _ClassVar[int]
    SPLITS_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    job: _types_pb2.JobSpec
    splits: _containers.RepeatedCompositeFieldContainer[_types_pb2.InputSplit]
    def __init__(self, job_id: _Optional[str] = ..., job: _Optional[_Union[_types_pb2.JobSpec, _Mapping]] = ..., splits: _Optional[_Iterable[_Union[_types_pb2.InputSplit, _Mapping]]] = ...) -> None: ...

class TaskAssigned(_message.Message):
    __slots__ = ("attempt", "worker_id", "locality")
    ATTEMPT_FIELD_NUMBER: _ClassVar[int]
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    LOCALITY_FIELD_NUMBER: _ClassVar[int]
    attempt: _types_pb2.TaskAttemptId
    worker_id: str
    locality: _types_pb2.Locality
    def __init__(self, attempt: _Optional[_Union[_types_pb2.TaskAttemptId, _Mapping]] = ..., worker_id: _Optional[str] = ..., locality: _Optional[_Union[_types_pb2.Locality, str]] = ...) -> None: ...

class WorkerLost(_message.Message):
    __slots__ = ("worker_id",)
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    def __init__(self, worker_id: _Optional[str] = ...) -> None: ...

class Restarted(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...
