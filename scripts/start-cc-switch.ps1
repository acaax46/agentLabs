param(
    [switch]$Hidden,
    [int]$DebugPort = 0,
    [string]$Python = 'python'
)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$installRoot = Join-Path $projectRoot '.tools/cc-switch/4.0.18'
$executable = Get-ChildItem -LiteralPath $installRoot -Recurse -Filter 'cc-switch.exe' | Select-Object -First 1
if (-not $executable) { throw 'CC Switch portable installation is missing. See runtime/README.md.' }
if (Get-Process -Name 'cc-switch' -ErrorAction SilentlyContinue) {
    throw 'CC Switch is already running. Close it before opening the isolated AgentLab profile.'
}
$pathPreferences = Join-Path $env:APPDATA 'com.ccswitch.desktop/app_paths.json'
if (Test-Path -LiteralPath $pathPreferences) {
    $preferences = Get-Content -Raw -LiteralPath $pathPreferences | ConvertFrom-Json
    if ($preferences.app_config_dir_override) {
        throw 'An existing CC Switch configuration-directory override takes precedence. Use the isolated Docker management profile instead.'
    }
}
$profileDirectory = Join-Path $projectRoot '.cache/cc-switch/profile'
New-Item -ItemType Directory -Force -Path $profileDirectory | Out-Null
& $Python (Join-Path $projectRoot 'runtime/cc-switch/profile_tool.py') seed --directory $profileDirectory
if ($LASTEXITCODE -ne 0) { throw 'Could not prepare the isolated AgentLab profile.' }
$launch = [System.Diagnostics.ProcessStartInfo]::new()
$launch.FileName = $executable.FullName
$launch.WorkingDirectory = $installRoot
$launch.UseShellExecute = $false
$launch.EnvironmentVariables['CC_SWITCH_TEST_HOME'] = $profileDirectory
if ($DebugPort -gt 0) {
    $launch.EnvironmentVariables['WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS'] = '--remote-debugging-port=' + $DebugPort
}
if ($Hidden) { $launch.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden }
$process = [System.Diagnostics.Process]::Start($launch)
[pscustomobject]@{ ProcessId = $process.Id; Profile = $profileDirectory; Version = '4.0.18' }
