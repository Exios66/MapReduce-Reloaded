package contract

import (
	"bytes"
	"encoding/hex"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	mrpb "github.com/exios66/mapreduce-reloaded/go/gen/mapreduce/v1"
)

// These literal vectors are also checked by Python's test_contract_messages.py.
// They protect wire compatibility independently of either serializer.
func TestSharedWireVectors(t *testing.T) {
	cases := []struct {
		name    string
		message proto.Message
		wireHex string
	}{
		{"no-assignment", &mrpb.RequestTaskResponse{}, ""},
		{"empty-map", &mrpb.RequestTaskResponse{Assignment: &mrpb.RequestTaskResponse_MapTask{MapTask: &mrpb.MapTask{}}}, "0a00"},
		{"empty-reduce", &mrpb.RequestTaskResponse{Assignment: &mrpb.RequestTaskResponse_ReduceTask{ReduceTask: &mrpb.ReduceTask{}}}, "1200"},
		{"empty-wait", &mrpb.RequestTaskResponse{Assignment: &mrpb.RequestTaskResponse_Wait{Wait: &mrpb.WaitInstruction{}}}, "1a00"},
		{"shutdown", &mrpb.RequestTaskResponse{Assignment: &mrpb.RequestTaskResponse_Shutdown{Shutdown: &mrpb.ShutdownInstruction{}}}, "2200"},
		{"unknown-failed-record", &mrpb.TaskFailure{}, ""},
		{"failed-record-zero", &mrpb.TaskFailure{FailedRecord: proto.Uint64(0)}, "1000"},
		{"failed-record-max", &mrpb.TaskFailure{FailedRecord: proto.Uint64(1<<64 - 1)}, "10ffffffffffffffffff01"},
		{"resume-offset-max", &mrpb.FetchPartitionRequest{Offset: 1<<64 - 1}, "20ffffffffffffffffff01"},
		{"task-index-max", &mrpb.TaskId{Index: 1<<32 - 1}, "18ffffffff0f"},
		{"binary-shuffle-chunk", &mrpb.FetchPartitionResponse{Data: []byte{0, 0xff, 0x80, '\n'}}, "0a0400ff800a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := hex.DecodeString(tc.wireHex)
			if err != nil {
				t.Fatal(err)
			}
			got, err := proto.MarshalOptions{Deterministic: true}.Marshal(tc.message)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("wire bytes: got %x, want %x", got, want)
			}
			decoded := tc.message.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(want, decoded); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(decoded, tc.message) {
				t.Fatalf("decoded %v, want %v", decoded, tc.message)
			}
		})
	}
}

func TestAssignmentOneofLastFieldWins(t *testing.T) {
	fields := []protoreflect.Name{"map_task", "reduce_task", "wait", "shutdown"}
	for i, name := range fields {
		t.Run(string(name), func(t *testing.T) {
			// Every pair of distinct assignment fields must select the last tag,
			// including shutdown, whose empty payload still carries presence.
			for j, previous := range fields {
				if i == j {
					continue
				}
				wire := []byte{byte((j+1)<<3 | 2), 0, byte((i+1)<<3 | 2), 0}
				var response mrpb.RequestTaskResponse
				if err := proto.Unmarshal(wire, &response); err != nil {
					t.Fatal(err)
				}
				message := response.ProtoReflect()
				oneof := message.Descriptor().Oneofs().ByName("assignment")
				if got := message.WhichOneof(oneof); got == nil || got.Name() != name {
					t.Fatalf("after %s then %s, selected %v", previous, name, got)
				}
				if message.Has(message.Descriptor().Fields().ByName(previous)) {
					t.Fatalf("previous assignment %s is still present", previous)
				}
			}
		})
	}
}

func TestReportOutcomeReplacementPreservesAttempt(t *testing.T) {
	attempt := &mrpb.TaskAttemptId{
		Task: &mrpb.TaskId{JobId: "job-1", Kind: mrpb.TaskKind_TASK_KIND_MAP, Index: 7}, Attempt: 2,
	}
	report := &mrpb.ReportTaskRequest{
		WorkerId: "worker-1", Attempt: attempt,
		Outcome: &mrpb.ReportTaskRequest_Success{Success: &mrpb.TaskSuccess{Counters: &mrpb.Counters{Values: map[string]int64{"records": 5}}}},
	}
	wire, err := proto.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	// Field 4 (failure), containing field 2 (failed_record) explicitly set to 0.
	wire = append(wire, 0x22, 2, 0x10, 0)
	var decoded mrpb.ReportTaskRequest
	if err := proto.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetSuccess() != nil || decoded.GetFailure() == nil || decoded.GetFailure().FailedRecord == nil {
		t.Fatalf("failure did not replace success with record-zero presence: %v", &decoded)
	}
	if decoded.GetFailure().GetFailedRecord() != 0 || !proto.Equal(decoded.GetAttempt(), attempt) || decoded.GetWorkerId() != "worker-1" {
		t.Fatalf("report identity or failed record changed: %v", &decoded)
	}
	// Reversing the outcome must also clear the failure.
	if err := proto.Unmarshal(append(wire, 0x1a, 0), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.GetFailure() != nil || decoded.GetSuccess() == nil {
		t.Fatalf("success did not replace failure: %v", &decoded)
	}
}

func TestFailedRecordPresence(t *testing.T) {
	unknown := &mrpb.TaskFailure{}
	first := &mrpb.TaskFailure{FailedRecord: proto.Uint64(0)}
	if unknown.GetFailedRecord() != first.GetFailedRecord() {
		t.Fatal("absent and explicit zero must both read as zero")
	}
	if proto.Equal(unknown, first) {
		t.Fatal("unknown record must remain distinct from explicit record zero")
	}
	first.FailedRecord = nil
	if !proto.Equal(unknown, first) {
		t.Fatal("clearing failed_record must restore absence")
	}
}

func TestContractMessageRoundTrips(t *testing.T) {
	cases := []struct {
		name    string
		message proto.Message
		text    string
	}{
		{"reduce-task", &mrpb.ReduceTask{}, `
			attempt { task { job_id: "job-1" kind: TASK_KIND_REDUCE index: 3 } attempt: 4 }
			job {
				program: "grep" inputs: "file:///a" inputs: "file:///b" output: "s3://bucket/out"
				num_reduce_tasks: 4 input_format: "text" output_format: "kv" partitioner: "custom"
				use_combiner: true params { key: "pattern" value: "λ" } params { key: "mode" value: "exact" }
			}
			sources { map_index: 0 partition: 3 worker_address: "worker:7070" size_bytes: 9007199254740993 }
			sources { map_index: 1 partition: 3 uri: "s3://bucket/part" size_bytes: 0 }
			skip_records: 0 skip_records: 9007199254740993 skip_records: 18446744073709551615
		`},
		{"worker-registration", &mrpb.RegisterWorkerRequest{}, `
			worker { shuffle_address: "worker:7070" host: "worker" zone: "rack-a" slots: 4 }
		`},
		{"heartbeat-interval", &mrpb.RegisterWorkerResponse{}, `
			worker_id: "worker-1" heartbeat_interval { seconds: 2 nanos: 500000000 }
		`},
		{"wait-duration", &mrpb.WaitInstruction{}, `retry_after { nanos: 500000000 }`},
		{"heartbeat-progress", &mrpb.HeartbeatRequest{}, `
			worker_id: "worker-1"
			running { attempt { task { job_id: "job-1" kind: TASK_KIND_MAP } } fraction_done: 0 }
			running { attempt { task { job_id: "job-1" kind: TASK_KIND_MAP } attempt: 1 } fraction_done: 0.5 }
			running { attempt { task { job_id: "job-1" kind: TASK_KIND_REDUCE } } fraction_done: 1 }
		`},
		{"heartbeat-cancellation", &mrpb.HeartbeatResponse{}, `
			cancel { task { job_id: "job-1" kind: TASK_KIND_MAP index: 7 } attempt: 1 }
			cancel { task { job_id: "job-1" kind: TASK_KIND_MAP index: 7 } attempt: 2 }
			reregister: true
		`},
		{"map-success", &mrpb.TaskSuccess{}, `
			outputs { map_index: 7 partition: 0 worker_address: "worker:7070" size_bytes: 0 }
			outputs { map_index: 7 partition: 1 worker_address: "worker:7070" size_bytes: 18446744073709551615 }
			counters { values { key: "records" value: 10 } }
		`},
		{"reduce-success", &mrpb.TaskSuccess{}, `counters { values { key: "records" value: 0 } }`},
		{"job-failure", &mrpb.GetJobStatusResponse{}, `
			job_id: "job-1" state: JOB_STATE_FAILED map_tasks_total: 10 map_tasks_completed: 9
			reduce_tasks_total: 4 reduce_tasks_completed: 2 error: "too many bad records"
			counters {
				values { key: "min" value: -9223372036854775808 }
				values { key: "max" value: 9223372036854775807 }
				values { key: "empty" value: 0 }
			}
		`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := prototext.Unmarshal([]byte(tc.text), tc.message); err != nil {
				t.Fatal(err)
			}
			wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(tc.message)
			if err != nil {
				t.Fatal(err)
			}
			decoded := tc.message.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(wire, decoded); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(decoded, tc.message) {
				t.Fatalf("round trip changed message:\n got %v\nwant %v", decoded, tc.message)
			}
		})
	}
}

func TestDeterministicMapSerialization(t *testing.T) {
	first := &mrpb.JobSpec{Params: map[string]string{}}
	first.Params["z"] = "last"
	first.Params["a"] = "first"
	second := &mrpb.JobSpec{Params: map[string]string{}}
	second.Params["a"] = "first"
	second.Params["z"] = "last"
	left, err := proto.MarshalOptions{Deterministic: true}.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := proto.MarshalOptions{Deterministic: true}.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatalf("map insertion order changed deterministic bytes: %x != %x", left, right)
	}
}

func TestUnknownFieldsSurviveForwarding(t *testing.T) {
	for _, message := range []proto.Message{
		&mrpb.TaskId{JobId: "job"}, &mrpb.HeartbeatResponse{Reregister: true},
		&mrpb.FetchPartitionRequest{JobId: "job", Offset: 17},
	} {
		t.Run(string(message.ProtoReflect().Descriptor().Name()), func(t *testing.T) {
			wire, err := proto.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			unknown := []byte{0xa0, 0x06, 7} // Future field 100, varint 7.
			decoded := message.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(append(wire, unknown...), decoded); err != nil {
				t.Fatal(err)
			}
			forwarded, err := proto.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decoded.ProtoReflect().GetUnknown(), unknown) || !bytes.HasSuffix(forwarded, unknown) {
				t.Fatalf("unknown field lost when forwarding: %x", forwarded)
			}
			decoded.ProtoReflect().SetUnknown(nil)
			if !proto.Equal(decoded, message) {
				t.Fatalf("known fields changed: %v", decoded)
			}
		})
	}
}

func TestFutureEnumValuesSurviveRoundTrip(t *testing.T) {
	for _, message := range []proto.Message{
		&mrpb.TaskId{Kind: mrpb.TaskKind(99)}, &mrpb.GetJobStatusResponse{State: mrpb.JobState(99)},
	} {
		t.Run(string(message.ProtoReflect().Descriptor().Name()), func(t *testing.T) {
			wire, err := proto.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			decoded := message.ProtoReflect().New().Interface()
			if err := proto.Unmarshal(wire, decoded); err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(decoded, message) {
				t.Fatalf("future enum value lost: %v", decoded)
			}
		})
	}
}

func TestMalformedWireDataIsRejected(t *testing.T) {
	for _, message := range []proto.Message{&mrpb.RequestTaskResponse{}, &mrpb.FetchPartitionResponse{}} {
		t.Run(string(message.ProtoReflect().Descriptor().Name()), func(t *testing.T) {
			for _, wire := range [][]byte{{0}, {0x0a, 5, 'a', 'b'}, {0x80}} {
				if err := proto.Unmarshal(wire, message); err == nil {
					t.Errorf("accepted malformed wire data %x", wire)
				}
			}
		})
	}
}

func TestNilMessageGettersReturnDefaults(t *testing.T) {
	var response *mrpb.RequestTaskResponse
	if response.GetAssignment() != nil || response.GetMapTask() != nil || response.GetReduceTask() != nil || response.GetWait() != nil || response.GetShutdown() != nil {
		t.Fatal("nil response must have no assignment")
	}
	var failure *mrpb.TaskFailure
	if failure.GetFailedRecord() != 0 || failure.GetMessage() != "" {
		t.Fatal("nil failure getters must return defaults")
	}
	var task *mrpb.MapTask
	if task.GetAttempt().GetTask().GetKind() != mrpb.TaskKind_TASK_KIND_UNSPECIFIED || task.GetJob().GetSplitSizeBytes() != 0 {
		t.Fatal("nil nested task getters must return defaults")
	}
}
