import anyio
import httpx
from fastapi import APIRouter, Depends, HTTPException, Request, Response

from app.auth.deps import require_role
from app.models import UserRole

router = APIRouter(prefix="/rclone", tags=["rclone-proxy"])

PROXY_METHODS = ["GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS", "HEAD"]
READ_METHODS = ["GET", "HEAD", "OPTIONS"]

STRIPPED_REQUEST_HEADERS = {"host", "authorization", "content-length"}
STRIPPED_RESPONSE_HEADERS = {
    "content-encoding",
    "content-length",
    "transfer-encoding",
    "connection",
    "keep-alive",
}


async def _forward(request: Request, path: str) -> Response:
    client: httpx.Client = request.app.state.proxy_client
    body = await request.body()
    headers = {
        key: value
        for key, value in request.headers.items()
        if key.lower() not in STRIPPED_REQUEST_HEADERS
    }

    def call() -> httpx.Response:
        return client.request(
            request.method,
            f"/{path}",
            params=request.query_params,
            content=body,
            headers=headers,
        )

    try:
        upstream = await anyio.to_thread.run_sync(call)
    except httpx.HTTPError as exc:
        raise HTTPException(status_code=502, detail=f"rclone rcd unreachable: {exc}") from exc

    response_headers = {
        key: value
        for key, value in upstream.headers.items()
        if key.lower() not in STRIPPED_RESPONSE_HEADERS
    }
    return Response(
        content=upstream.content,
        status_code=upstream.status_code,
        headers=response_headers,
    )


# Reads (stats, job status) — all authenticated users.
# Writes (config/create, config/delete) — admin only: they inject upstream credentials.
@router.api_route(
    "/{path:path}",
    methods=READ_METHODS,
    include_in_schema=False,
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit, UserRole.view))],
)
async def proxy_path_read(request: Request, path: str):
    return await _forward(request, path)


@router.api_route(
    "",
    methods=READ_METHODS,
    include_in_schema=False,
    dependencies=[Depends(require_role(UserRole.admin, UserRole.edit, UserRole.view))],
)
async def proxy_root_read(request: Request):
    return await _forward(request, "")


@router.api_route(
    "/{path:path}",
    methods=["POST", "PUT", "DELETE", "PATCH"],
    include_in_schema=False,
    dependencies=[Depends(require_role(UserRole.admin))],
)
async def proxy_path_write(request: Request, path: str):
    return await _forward(request, path)


@router.api_route(
    "",
    methods=["POST", "PUT", "DELETE", "PATCH"],
    include_in_schema=False,
    dependencies=[Depends(require_role(UserRole.admin))],
)
async def proxy_root_write(request: Request):
    return await _forward(request, "")
