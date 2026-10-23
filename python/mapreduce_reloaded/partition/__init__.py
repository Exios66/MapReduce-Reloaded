"""Decides which reduce task receives each intermediate key."""

from mapreduce_reloaded.partition.hash import fnv1a64, partition

__all__ = ["fnv1a64", "partition"]
