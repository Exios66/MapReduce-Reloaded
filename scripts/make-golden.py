"""Regenerates testdata/proto/*.binpb from the human-readable *.txtpb files.

Only needed after editing a .txtpb file. The Go and Python test suites both
check that their own serialization of each .txtpb matches the .binpb exactly.
"""

from pathlib import Path

from google.protobuf import text_format

from mapreduce_reloaded.gen.mapreduce.v1 import coordinator_pb2

GOLDEN = Path(__file__).resolve().parent.parent / "testdata" / "proto"

VECTORS = {"map_task": coordinator_pb2.MapTask}

for name, message_type in VECTORS.items():
    message = text_format.Parse((GOLDEN / f"{name}.txtpb").read_text(), message_type())
    (GOLDEN / f"{name}.binpb").write_bytes(message.SerializeToString(deterministic=True))
    print(f"wrote {name}.binpb")
