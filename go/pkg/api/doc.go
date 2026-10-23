// Package api is the public programming model for MapReduce-Reloaded.
//
// It mirrors §2 of Dean & Ghemawat (OSDI 2004):
//
//	map    (k1, v1)       -> list(k2, v2)
//	reduce (k2, list(v2)) -> list(v2)
//
// The paper's canonical example counts words:
//
//	map(String key, String value):
//	  // key: document name
//	  // value: document contents
//	  for each word w in value:
//	    EmitIntermediate(w, "1");
//
//	reduce(String key, Iterator values):
//	  // key: a word
//	  // values: a list of counts
//	  int result = 0;
//	  for each v in values:
//	    result += ParseInt(v);
//	  Emit(AsString(result));
//
// A user program implements Mapper and Reducer (see the WordCount example).
// Everything else in the system exists to run those two functions reliably
// across many machines.
//
// Keys and values are raw bytes throughout. User code decides how to encode
// its data; the framework only sorts, partitions and moves bytes. See
// docs/design/0005-programming-api.md.
package api
