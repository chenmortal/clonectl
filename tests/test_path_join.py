"""Tests for path joining (DataSource.path + task.{src,dst}_path)."""
from app.services.runner import _join_ds_path


def test_both_empty():
    assert _join_ds_path("", "") == ""


def test_only_ds_path():
    assert _join_ds_path("bucket/prefix", "") == "bucket/prefix"


def test_only_task_subpath():
    assert _join_ds_path("", "sub/dir") == "sub/dir"


def test_both_present_basic():
    assert _join_ds_path("bucket", "sub") == "bucket/sub"


def test_trailing_slash_on_ds_stripped():
    assert _join_ds_path("bucket/prefix/", "sub") == "bucket/prefix/sub"


def test_leading_slash_on_subpath_stripped():
    assert _join_ds_path("bucket/prefix", "/sub") == "bucket/prefix/sub"


def test_both_slashes_normalized():
    # Leading slash on ds_path is preserved (rclone accepts it; not stripped).
    assert _join_ds_path("/bucket/prefix/", "/sub/") == "/bucket/prefix/sub/"


def test_ds_path_root_only():
    """A ds_path of just '/' (the default) means 'no prefix'; return subpath."""
    assert _join_ds_path("/", "sub") == "sub"
    assert _join_ds_path("/", "/sub") == "sub"


def test_ds_path_none_treated_as_empty():
    assert _join_ds_path(None, "sub") == "sub"
    assert _join_ds_path(None, "") == ""


def test_deep_paths():
    assert _join_ds_path("a/b/c", "d/e/f") == "a/b/c/d/e/f"


def test_task_subpath_none_treated_as_empty():
    assert _join_ds_path("bucket", None) == "bucket"
