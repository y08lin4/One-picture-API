@echo off
setlocal

cd /d "%~dp0One picture-API" || (
  echo [ERROR] 无法进入项目目录：One picture-API
  pause
  exit /b 1
)

echo [INFO] 正在启动 One-picture-API...
go run .

if errorlevel 1 (
  echo.
  echo [ERROR] 启动失败，请检查 Go 环境与代码。
) else (
  echo.
  echo [INFO] 服务已退出。
)

pause
