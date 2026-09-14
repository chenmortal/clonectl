curl -X POST http://localhost:5572/job/status \
-u "admin:6051" \
-H "Content-Type: application/json" \
-d '{
    "jobid": 468
}'

curl -X POST http://localhost:5572/core/stats \
-u "admin:6051" \
-H "Content-Type: application/json" \
-d '{"group": "job/468"}'
# {
#         "duration": 0,
#         "endTime": "0001-01-01T00:00:00Z",
#         "error": "",
#         "executeId": "72b82a4f-83d8-46aa-b38f-2017379d3611",
#         "finished": false,
#         "group": "job/468",
#         "id": 468,
#         "output": null,
#         "startTime": "2026-08-15T20:31:40.297004+08:00",
#         "success": false
# }
# {
#         "bytes": 3212509184,
#         "checks": 4,
#         "deletedDirs": 0,
#         "deletes": 0,
#         "elapsedTime": 149.478007709,
#         "errors": 0,
#         "eta": 99,
#         "fatalError": false,
#         "listed": 11,
#         "renames": 0,
#         "retryError": false,
#         "serverSideCopies": 0,
#         "serverSideCopyBytes": 0,
#         "serverSideMoveBytes": 0,
#         "serverSideMoves": 0,
#         "speed": 25798704.728441067,
#         "totalBytes": 5770968007,
#         "totalChecks": 4,
#         "totalTransfers": 1,
#         "transferTime": 149.46593675,
#         "transferring": [
#                 {
#                         "bytes": 3212509184,
#                         "dstFs": "remote-s3:data",
#                         "eta": 99,
#                         "group": "job/468",
#                         "name": "花心.2016.1080P.日语中字/花芯.A.Flower.Aflame..2016.JAPANESE.1080p.AMZN.WEBRip.DDP5.1.x264-ARiN-1.mp4",
#                         "percentage": 55,
#                         "size": 5770968007,
#                         "speed": 21513057.65249361,
#                         "speedAvg": 25834450.182059098,
#                         "srcFs": "local-s3:data"
#                 }
#         ],
#         "transfers": 0
# }