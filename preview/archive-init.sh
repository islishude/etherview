#!/bin/sh
set -eu
rc alias set preview http://object-storage:9000 "$RUSTFS_ACCESS_KEY" "$RUSTFS_SECRET_KEY"
rc mb --ignore-existing preview/etherview-preview-blobs
rc admin user add preview preview-reader "$PREVIEW_BLOB_READ_SECRET"
rc admin user add preview preview-writer "$PREVIEW_BLOB_WRITE_SECRET"
rc admin policy create preview preview-blob-read /policies/read.json
rc admin policy create preview preview-blob-write /policies/write.json
rc admin policy attach preview preview-blob-read --user preview-reader
rc admin policy attach preview preview-blob-write --user preview-writer
