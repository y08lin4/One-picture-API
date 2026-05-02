@echo off
setlocal

cd /d "%~dp0One picture-API" || (
  echo [ERROR] Cannot enter project directory: One picture-API
  pause
  exit /b 1
)

if not exist tokens.json if "%OPAPI_TOKENS%"=="" (
  echo [ERROR] Missing login token configuration.
  echo Copy tokens.example.json to tokens.json and replace the token,
  echo or set OPAPI_TOKENS before starting.
  pause
  exit /b 1
)

echo [INFO] Starting One-picture-API...
go run .

if errorlevel 1 (
  echo.
  echo [ERROR] Start failed. Please check Go and configuration.
) else (
  echo.
  echo [INFO] Service exited.
)

pause
