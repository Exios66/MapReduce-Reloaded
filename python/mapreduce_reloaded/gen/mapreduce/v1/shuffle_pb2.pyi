from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class FetchPartitionRequest(_message.Message):
    __slots__ = ("job_id", "map_index", "partition", "offset")
    JOB_ID_FIELD_NUMBER: _ClassVar[int]
    MAP_INDEX_FIELD_NUMBER: _ClassVar[int]
    PARTITION_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    job_id: str
    map_index: int
    partition: int
    offset: int
    def __init__(self, job_id: _Optional[str] = ..., map_index: _Optional[int] = ..., partition: _Optional[int] = ..., offset: _Optional[int] = ...) -> None: ...

class FetchPartitionResponse(_message.Message):
    __slots__ = ("data",)
    DATA_FIELD_NUMBER: _ClassVar[int]
    data: bytes
    def __init__(self, data: _Optional[bytes] = ...) -> None: ...
