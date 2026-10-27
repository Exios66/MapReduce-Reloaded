import datetime

from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from mapreduce_reloaded.gen.mapreduce.v1 import types_pb2 as _types_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class JobState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    JOB_STATE_UNSPECIFIED: _ClassVar[JobState]
    JOB_STATE_PENDING: _ClassVar[JobState]
    JOB_STATE_MAPPING: _ClassVar[JobState]
    JOB_STATE_REDUCING: _ClassVar[JobState]
    JOB_STATE_SUCCEEDED: _ClassVar[JobState]
    JOB_STATE_FAILED: _ClassVar[JobState]
JOB_STATE_UNSPECIFIED: JobState
JOB_STATE_PENDING: JobState
JOB_STATE_MAPPING: JobState
JOB_STATE_REDUCING: JobState
JOB_STATE_SUCCEEDED: JobState
JOB_STATE_FAILED: JobState

class RegisterWorkerRequest(_message.Message):
    __slots__ = ("worker",)
    WORKER_FIELD_NUMBER: _ClassVar[int]
    worker: _types_pb2.WorkerInfo
    def __init__(self, worker: _Optional[_Union[_types_pb2.WorkerInfo, _Mapping]] = ...) -> None: ...

class RegisterWorkerResponse(_message.Message):
    __slots__ = ("worker_id", "heartbeat_interval")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    HEARTBEAT_INTERVAL_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    heartbeat_interval: _duration_pb2.Duration
    def __init__(self, worker_id: _Optional[str] = ..., heartbeat_interval: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class RequestTaskRequest(_message.Message):
    __slots__ = ("worker_id", "paused")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    PAUSED_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    paused: bool
    def __init__(self, worker_id: _Optional[str] = ..., paused: _Optional[bool] = ...) -> None: ...

class RequestTaskResponse(_message.Message):
    __slots__ = ("map_task", "reduce_task", "wait", "shutdown")
    MAP_TASK_FIELD_NUMBER: _ClassVar[int]
    REDUCE_TASK_FIELD_NUMBER: _ClassVar[int]
    WAIT_FIELD_NUMBER: _ClassVar[int]
    SHUTDOWN_FIELD_NUMBER: _ClassVar[int]
    map_task: MapTask
    reduce_task: ReduceTask
    wait: WaitInstruction
    shutdown: ShutdownInstruction
    def __init__(self, map_task: _Optional[_Union[MapTask, _Mapping]] = ..., reduce_task: _Optional[_Union[ReduceTask, _Mapping]] = ..., wait: _Optional[_Union[WaitInstruction, _Mapping]] = ..., shutdown: _Optional[_Union[ShutdownInstruction, _Mapping]] = ...) -> None: ...

class MapTask(_message.Message):
    __slots__ = ("attempt", "job", "split", "skip_records")
    ATTEMPT_FIELD_NUMBER: _ClassVar[int]
    JOB_FIELD_NUMBER: _ClassVar[int]
    SPLIT_FIELD_NUMBER: _ClassVar[int]
    SKIP_RECORDS_FIELD_NUMBER: _ClassVar[int]
    attempt: _types_pb2.TaskAttemptId
    job: _types_pb2.JobSpec
    split: _types_pb2.InputSplit
    skip_records: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, attempt: _Optional[_Union[_types_pb2.TaskAttemptId, _Mapping]] = ..., job: _Optional[_Union[_types_pb2.JobSpec, _Mapping]] = ..., split: _Optional[_Union[_types_pb2.InputSplit, _Mapping]] = ..., skip_records: _Optional[_Iterable[int]] = ...) -> None: ...

class ReduceTask(_message.Message):
    __slots__ = ("attempt", "job", "sources", "skip_records")
    ATTEMPT_FIELD_NUMBER: _ClassVar[int]
    JOB_FIELD_NUMBER: _ClassVar[int]
    SOURCES_FIELD_NUMBER: _ClassVar[int]
    SKIP_RECORDS_FIELD_NUMBER: _ClassVar[int]
    attempt: _types_pb2.TaskAttemptId
    job: _types_pb2.JobSpec
    sources: _containers.RepeatedCompositeFieldContainer[_types_pb2.PartitionLocation]
    skip_records: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, attempt: _Optional[_Union[_types_pb2.TaskAttemptId, _Mapping]] = ..., job: _Optional[_Union[_types_pb2.JobSpec, _Mapping]] = ..., sources: _Optional[_Iterable[_Union[_types_pb2.PartitionLocation, _Mapping]]] = ..., skip_records: _Optional[_Iterable[int]] = ...) -> None: ...

class WaitInstruction(_message.Message):
    __slots__ = ("retry_after",)
    RETRY_AFTER_FIELD_NUMBER: _ClassVar[int]
    retry_after: _duration_pb2.Duration
    def __init__(self, retry_after: _Optional[_Union[datetime.timedelta, _duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class ShutdownInstruction(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ReportTaskRequest(_message.Message):
    __slots__ = ("worker_id", "attempt", "success", "failure")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    ATTEMPT_FIELD_NUMBER: _ClassVar[int]
    SUCCESS_FIELD_NUMBER: _ClassVar[int]
    FAILURE_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    attempt: _types_pb2.TaskAttemptId
    success: TaskSuccess
    failure: TaskFailure
    def __init__(self, worker_id: _Optional[str] = ..., attempt: _Optional[_Union[_types_pb2.TaskAttemptId, _Mapping]] = ..., success: _Optional[_Union[TaskSuccess, _Mapping]] = ..., failure: _Optional[_Union[TaskFailure, _Mapping]] = ...) -> None: ...

class TaskSuccess(_message.Message):
    __slots__ = ("outputs", "counters")
    OUTPUTS_FIELD_NUMBER: _ClassVar[int]
    COUNTERS_FIELD_NUMBER: _ClassVar[int]
    outputs: _containers.RepeatedCompositeFieldContainer[_types_pb2.PartitionLocation]
    counters: _types_pb2.Counters
    def __init__(self, outputs: _Optional[_Iterable[_Union[_types_pb2.PartitionLocation, _Mapping]]] = ..., counters: _Optional[_Union[_types_pb2.Counters, _Mapping]] = ...) -> None: ...

class TaskFailure(_message.Message):
    __slots__ = ("message", "failed_record", "lost_output")
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    FAILED_RECORD_FIELD_NUMBER: _ClassVar[int]
    LOST_OUTPUT_FIELD_NUMBER: _ClassVar[int]
    message: str
    failed_record: int
    lost_output: _types_pb2.PartitionLocation
    def __init__(self, message: _Optional[str] = ..., failed_record: _Optional[int] = ..., lost_output: _Optional[_Union[_types_pb2.PartitionLocation, _Mapping]] = ...) -> None: ...

class ReportTaskResponse(_message.Message):
    __slots__ = ("accepted",)
    ACCEPTED_FIELD_NUMBER: _ClassVar[int]
    accepted: bool
    def __init__(self, accepted: _Optional[bool] = ...) -> None: ...

class HeartbeatRequest(_message.Message):
    __slots__ = ("worker_id", "running")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    RUNNING_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    running: _containers.RepeatedCompositeFieldContainer[TaskProgress]
    def __init__(self, worker_id: _Optional[str] = ..., running: _Optional[_Iterable[_Union[TaskProgress, _Mapping]]] = ...) -> None: ...

class TaskProgress(_message.Message):
    __slots__ = ("attempt", "fraction_done")
    ATTEMPT_FIELD_NUMBER: _ClassVar[int]
    FRACTION_DONE_FIELD_NUMBER: _ClassVar[int]
    attempt: _types_pb2.TaskAttemptId
    fraction_done: float
    def __init__(self, attempt: _Optional[_Union[_types_pb2.TaskAttemptId, _Mapping]] = ..., fraction_done: _Optional[float] = ...) -> None: ...

class HeartbeatResponse(_message.Message):
    __slots__ = ("cancel", "reregister", "active_jobs")
    CANCEL_FIELD_NUMBER: _ClassVar[int]
    REREGISTER_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_JOBS_FIELD_NUMBER: _ClassVar[int]
    cancel: _containers.RepeatedCompositeFieldContainer[_types_pb2.TaskAttemptId]
    reregister: bool
    active_jobs: ActiveJobs
    def __init__(self, cancel: _Optional[_Iterable[_Union[_types_pb2.TaskAttemptId, _Mapping]]] = ..., reregister: _Optional[bool] = ..., active_jobs: _Optional[_Union[ActiveJobs, _Mapping]] = ...) -> None: ...

class ActiveJobs(_message.Message):
    __slots__ = ("job_ids",)
    JOB_IDS_FIELD_NUMBER: _ClassVar[int]
    job_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, job_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class SubmitJobRequest(_message.Message):
    __slots__ = ("job",)
    JOB_FIELD_NUMBER: _ClassVar[int]
    job: _types_pb2.JobSpec
    def __init__(self, job: _Optional[_Union[_types_pb2.JobSpec, _Mapping]] = ...) -> None: ...

class SubmitJobResponse(_message.Message):
    __slots__ = ("job_id",)
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    def __init__(self, job_id: _Optional[str] = ...) -> None: ...

class GetJobStatusRequest(_message.Message):
    __slots__ = ("job_id",)
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    def __init__(self, job_id: _Optional[str] = ...) -> None: ...

class GetJobStatusResponse(_message.Message):
    __slots__ = ("job_id", "state", "map_tasks_total", "map_tasks_completed", "reduce_tasks_total", "reduce_tasks_completed", "counters", "error", "map_tasks_host_local", "map_tasks_zone_local", "submitted_at", "finished_at", "backup_attempts", "backup_wins")
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    MAP_TASKS_TOTAL_FIELD_NUMBER: _ClassVar[int]
    MAP_TASKS_COMPLETED_FIELD_NUMBER: _ClassVar[int]
    REDUCE_TASKS_TOTAL_FIELD_NUMBER: _ClassVar[int]
    REDUCE_TASKS_COMPLETED_FIELD_NUMBER: _ClassVar[int]
    COUNTERS_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    MAP_TASKS_HOST_LOCAL_FIELD_NUMBER: _ClassVar[int]
    MAP_TASKS_ZONE_LOCAL_FIELD_NUMBER: _ClassVar[int]
    SUBMITTED_AT_FIELD_NUMBER: _ClassVar[int]
    FINISHED_AT_FIELD_NUMBER: _ClassVar[int]
    BACKUP_ATTEMPTS_FIELD_NUMBER: _ClassVar[int]
    BACKUP_WINS_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    state: JobState
    map_tasks_total: int
    map_tasks_completed: int
    reduce_tasks_total: int
    reduce_tasks_completed: int
    counters: _types_pb2.Counters
    error: str
    map_tasks_host_local: int
    map_tasks_zone_local: int
    submitted_at: _timestamp_pb2.Timestamp
    finished_at: _timestamp_pb2.Timestamp
    backup_attempts: int
    backup_wins: int
    def __init__(self, job_id: _Optional[str] = ..., state: _Optional[_Union[JobState, str]] = ..., map_tasks_total: _Optional[int] = ..., map_tasks_completed: _Optional[int] = ..., reduce_tasks_total: _Optional[int] = ..., reduce_tasks_completed: _Optional[int] = ..., counters: _Optional[_Union[_types_pb2.Counters, _Mapping]] = ..., error: _Optional[str] = ..., map_tasks_host_local: _Optional[int] = ..., map_tasks_zone_local: _Optional[int] = ..., submitted_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., finished_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., backup_attempts: _Optional[int] = ..., backup_wins: _Optional[int] = ...) -> None: ...
