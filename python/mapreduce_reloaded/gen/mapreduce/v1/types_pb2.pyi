from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class TaskKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TASK_KIND_UNSPECIFIED: _ClassVar[TaskKind]
    TASK_KIND_MAP: _ClassVar[TaskKind]
    TASK_KIND_REDUCE: _ClassVar[TaskKind]

class TaskState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TASK_STATE_UNSPECIFIED: _ClassVar[TaskState]
    TASK_STATE_IDLE: _ClassVar[TaskState]
    TASK_STATE_IN_PROGRESS: _ClassVar[TaskState]
    TASK_STATE_COMPLETED: _ClassVar[TaskState]

class Locality(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    LOCALITY_UNSPECIFIED: _ClassVar[Locality]
    LOCALITY_HOST: _ClassVar[Locality]
    LOCALITY_ZONE: _ClassVar[Locality]
    LOCALITY_REMOTE: _ClassVar[Locality]
TASK_KIND_UNSPECIFIED: TaskKind
TASK_KIND_MAP: TaskKind
TASK_KIND_REDUCE: TaskKind
TASK_STATE_UNSPECIFIED: TaskState
TASK_STATE_IDLE: TaskState
TASK_STATE_IN_PROGRESS: TaskState
TASK_STATE_COMPLETED: TaskState
LOCALITY_UNSPECIFIED: Locality
LOCALITY_HOST: Locality
LOCALITY_ZONE: Locality
LOCALITY_REMOTE: Locality

class TaskId(_message.Message):
    __slots__ = ("job_id", "kind", "index")
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    INDEX_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    kind: TaskKind
    index: int
    def __init__(self, job_id: _Optional[str] = ..., kind: _Optional[_Union[TaskKind, str]] = ..., index: _Optional[int] = ...) -> None: ...

class TaskAttemptId(_message.Message):
    __slots__ = ("task", "attempt")
    TASK_FIELD_NUMBER: _ClassVar[int]
    ATTEMPT_FIELD_NUMBER: _ClassVar[int]
    task: TaskId
    attempt: int
    def __init__(self, task: _Optional[_Union[TaskId, _Mapping]] = ..., attempt: _Optional[int] = ...) -> None: ...

class InputSplit(_message.Message):
    __slots__ = ("uri", "offset", "length", "locations")
    URI_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    LENGTH_FIELD_NUMBER: _ClassVar[int]
    LOCATIONS_FIELD_NUMBER: _ClassVar[int]
    uri: str
    offset: int
    length: int
    locations: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, uri: _Optional[str] = ..., offset: _Optional[int] = ..., length: _Optional[int] = ..., locations: _Optional[_Iterable[str]] = ...) -> None: ...

class PartitionLocation(_message.Message):
    __slots__ = ("map_index", "partition", "worker_address", "size_bytes", "uri")
    MAP_INDEX_FIELD_NUMBER: _ClassVar[int]
    PARTITION_FIELD_NUMBER: _ClassVar[int]
    WORKER_ADDRESS_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    URI_FIELD_NUMBER: _ClassVar[int]
    map_index: int
    partition: int
    worker_address: str
    size_bytes: int
    uri: str
    def __init__(self, map_index: _Optional[int] = ..., partition: _Optional[int] = ..., worker_address: _Optional[str] = ..., size_bytes: _Optional[int] = ..., uri: _Optional[str] = ...) -> None: ...

class Counters(_message.Message):
    __slots__ = ("values",)
    class ValuesEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: int
        def __init__(self, key: _Optional[str] = ..., value: _Optional[int] = ...) -> None: ...
    VALUES_FIELD_NUMBER: _ClassVar[int]
    values: _containers.ScalarMap[str, int]
    def __init__(self, values: _Optional[_Mapping[str, int]] = ...) -> None: ...

class JobSpec(_message.Message):
    __slots__ = ("program", "inputs", "output", "split_size_bytes", "num_reduce_tasks", "input_format", "output_format", "partitioner", "use_combiner", "params", "max_skipped_records", "intermediate_codec", "output_codec")
    class ParamsEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    PROGRAM_FIELD_NUMBER: _ClassVar[int]
    INPUTS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    SPLIT_SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    NUM_REDUCE_TASKS_FIELD_NUMBER: _ClassVar[int]
    INPUT_FORMAT_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_FORMAT_FIELD_NUMBER: _ClassVar[int]
    PARTITIONER_FIELD_NUMBER: _ClassVar[int]
    USE_COMBINER_FIELD_NUMBER: _ClassVar[int]
    PARAMS_FIELD_NUMBER: _ClassVar[int]
    MAX_SKIPPED_RECORDS_FIELD_NUMBER: _ClassVar[int]
    INTERMEDIATE_CODEC_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_CODEC_FIELD_NUMBER: _ClassVar[int]
    program: str
    inputs: _containers.RepeatedScalarFieldContainer[str]
    output: str
    split_size_bytes: int
    num_reduce_tasks: int
    input_format: str
    output_format: str
    partitioner: str
    use_combiner: bool
    params: _containers.ScalarMap[str, str]
    max_skipped_records: int
    intermediate_codec: str
    output_codec: str
    def __init__(self, program: _Optional[str] = ..., inputs: _Optional[_Iterable[str]] = ..., output: _Optional[str] = ..., split_size_bytes: _Optional[int] = ..., num_reduce_tasks: _Optional[int] = ..., input_format: _Optional[str] = ..., output_format: _Optional[str] = ..., partitioner: _Optional[str] = ..., use_combiner: _Optional[bool] = ..., params: _Optional[_Mapping[str, str]] = ..., max_skipped_records: _Optional[int] = ..., intermediate_codec: _Optional[str] = ..., output_codec: _Optional[str] = ...) -> None: ...

class WorkerInfo(_message.Message):
    __slots__ = ("shuffle_address", "host", "zone", "slots")
    SHUFFLE_ADDRESS_FIELD_NUMBER: _ClassVar[int]
    HOST_FIELD_NUMBER: _ClassVar[int]
    ZONE_FIELD_NUMBER: _ClassVar[int]
    SLOTS_FIELD_NUMBER: _ClassVar[int]
    shuffle_address: str
    host: str
    zone: str
    slots: int
    def __init__(self, shuffle_address: _Optional[str] = ..., host: _Optional[str] = ..., zone: _Optional[str] = ..., slots: _Optional[int] = ...) -> None: ...
