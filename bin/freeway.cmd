@echo off
if exist "%~dp0..\runtime\node.exe" (
  "%~dp0..\runtime\node.exe" "%~dp0..\cli.mjs" %*
) else (
  node "%~dp0..\cli.mjs" %*
)
