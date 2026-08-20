"""认证路由：bootstrap 首管理员 / 登录 / 用户管理 / 当前用户。"""
from __future__ import annotations

from fastapi import APIRouter, HTTPException, Request, status
from pydantic import BaseModel, Field

from app.services import AppServices

router = APIRouter(prefix="/auth", tags=["auth"])


def _services(request: Request) -> AppServices:
    return request.app.state.services


def _current_user(request: Request) -> dict:
    user = getattr(request.state, "user", None)
    if user is None:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, "未登录或令牌无效")
    return user


class Credentials(BaseModel):
    username: str = Field(min_length=1, max_length=64)
    password: str = Field(min_length=1, max_length=256)


class CreateUserRequest(Credentials):
    role: str = "member"


class LoginResponse(BaseModel):
    token: str
    username: str
    role: str
    expires_at: str


class UserResponse(BaseModel):
    username: str
    role: str


@router.post("/bootstrap", response_model=UserResponse)
def bootstrap(body: Credentials, request: Request) -> UserResponse:
    """创建首个管理员；仅当用户库为空时可用。"""
    services = _services(request)
    if services.users.count_users() > 0:
        raise HTTPException(status.HTTP_409_CONFLICT, "已存在用户，请用管理员登录后创建账号")
    try:
        services.users.create_user(body.username, body.password, role="admin")
    except ValueError as exc:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, str(exc)) from exc
    return UserResponse(username=body.username, role="admin")


@router.post("/login", response_model=LoginResponse)
def login(body: Credentials, request: Request) -> LoginResponse:
    services = _services(request)
    user = services.users.verify(body.username, body.password)
    if user is None:
        raise HTTPException(status.HTTP_401_UNAUTHORIZED, "用户名或口令错误")
    token, expires_at = services.users.issue_token(user["username"])
    return LoginResponse(token=token, username=user["username"], role=user["role"], expires_at=expires_at)


@router.get("/me", response_model=UserResponse)
def me(request: Request) -> UserResponse:
    user = _current_user(request)
    return UserResponse(username=user["username"], role=user["role"])


@router.get("/users", response_model=list[UserResponse])
def list_users(request: Request) -> list[UserResponse]:
    _require_admin(request)
    services = _services(request)
    return [UserResponse(username=u["username"], role=u["role"]) for u in services.users.list_users()]


@router.post("/users", response_model=UserResponse, status_code=status.HTTP_201_CREATED)
def create_user(body: CreateUserRequest, request: Request) -> UserResponse:
    _require_admin(request)
    services = _services(request)
    try:
        services.users.create_user(body.username, body.password, role=body.role)
    except ValueError as exc:
        raise HTTPException(status.HTTP_422_UNPROCESSABLE_ENTITY, str(exc)) from exc
    return UserResponse(username=body.username, role=body.role)


def _require_admin(request: Request) -> None:
    user = _current_user(request)
    if user["role"] != "admin":
        raise HTTPException(status.HTTP_403_FORBIDDEN, "需要管理员权限")
