curl -X POST http://localhost:5572/sync/sync \
-u "admin:6051" \
-H "Content-Type: application/json" \
-d '{
    "srcFs": "local-s3:/data",
    "dstFs": "remote-s3:/data",
    "_async": true
}'