# curl -X POST http://localhost:5572/sync/sync \
#   -H "Content-Type: application/json" \
#   -d '{
#     "srcFs": "my-s3-storage:/my-bucket/source",
#     "dstFs": "my-oss-storage:/my-bucket/backup",
#     "_async": true
#   }'

curl -X POST http://localhost:5572/config/create \
-u "admin:6051" \
-H "Content-Type: application/json" \
-d '{
"name": "local-s3",
"type": "s3",
"parameters": {
    "provider": "Minio",
    "access_key_id": "rustfsadmin",
    "secret_access_key": "rustfsadmin",
    "endpoint": "http://127.0.0.1:9000"
}
}'

curl -X POST http://localhost:5572/config/create \
-u "admin:6051" \
-H "Content-Type: application/json" \
-d '{
"name": "remote-s3",
"type": "s3",
"parameters": {
    "provider": "Minio",
    "access_key_id": "rustfsadmin",
    "secret_access_key": "rustfsadmin",
    "endpoint": "http://10.126.126.1:9000"
}
}'