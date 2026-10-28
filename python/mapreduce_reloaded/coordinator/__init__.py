"""The master's state (§3.2), mirroring go/internal/coordinator: every
task's state and attempts, where each completed map task's output lives,
and which workers are alive.

Its types do no locking. The coordinator guards all of them with one lock;
see docs/design/0006-coordinator-concurrency.md.
"""

from mapreduce_reloaded.coordinator.registry import Registry, Worker
from mapreduce_reloaded.coordinator.tasktable import SKIP_AFTER, Task, TaskTable

__all__ = ["SKIP_AFTER", "Registry", "Task", "TaskTable", "Worker"]
