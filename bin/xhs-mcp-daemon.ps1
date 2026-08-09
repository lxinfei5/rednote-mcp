#Requires -Version 5.1
<#
.SYNOPSIS
  xiaohongshu-mcp 进程管理（Windows）

.DESCRIPTION
  与 bin/xhs-mcp-daemon.sh 对齐：HTTP MCP 监听 127.0.0.1:18060，
  cwd 固定为项目根（cookies.json 相对路径依赖）。
  状态目录默认 %USERPROFILE%\.xiaohongshu-mcp\（不进 git）。

  持久化策略：
  - start：Start-Process 脱离当前控制台，关终端不杀服务
  - install-autostart：注册当前用户「登录时」计划任务，重启后自动拉起
  - uninstall-autostart：移除计划任务

.EXAMPLE
  .\bin\xhs-mcp-daemon.ps1 start
  .\bin\xhs-mcp-daemon.ps1 stop
  .\bin\xhs-mcp-daemon.ps1 status
  .\bin\xhs-mcp-daemon.ps1 install-autostart
#>
param(
    [Parameter(Position = 0)]
    [ValidateSet(
        'start', 'stop', 'restart', 'status', 'health', 'health-check',
        'logs', 'cleanup', 'cleanup-camoufox', 'cleanup-rod',
        'install-autostart', 'uninstall-autostart', 'help'
    )]
    [string]$Command = 'status'
)

$ErrorActionPreference = 'Stop'

# --- paths -----------------------------------------------------------------
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = (Resolve-Path (Join-Path $ScriptDir '..')).Path

function Resolve-Bin {
    if ($env:XHS_MCP_BIN -and (Test-Path -LiteralPath $env:XHS_MCP_BIN)) {
        return (Resolve-Path -LiteralPath $env:XHS_MCP_BIN).Path
    }
    $candidates = @(
        (Join-Path $ProjectRoot 'bin\xhs-mcp.exe'),
        (Join-Path $ProjectRoot 'xiaohongshu-mcp.exe'),
        (Join-Path $ProjectRoot 'xiaohongshu-mcp')
    )
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c) {
            return (Resolve-Path -LiteralPath $c).Path
        }
    }
    return $candidates[0]
}

$Bin = Resolve-Bin
$StateHome = if ($env:XHS_MCP_STATE_HOME) { $env:XHS_MCP_STATE_HOME } else { Join-Path $env:USERPROFILE '.xiaohongshu-mcp' }
$LogDir = Join-Path $StateHome 'logs'
$PidDir = Join-Path $StateHome 'pids'
$PidFile = Join-Path $PidDir 'xiaohongshu-mcp.pid'
$LogFile = Join-Path $LogDir 'xiaohongshu-mcp.log'
$ErrLogFile = Join-Path $LogDir 'xiaohongshu-mcp.err.log'
$WrapperCmd = Join-Path $StateHome 'run-xhs-mcp.cmd'
$Port = 18060
$TaskName = 'xiaohongshu-mcp'

New-Item -ItemType Directory -Force -Path $LogDir, $PidDir | Out-Null

# --- helpers ---------------------------------------------------------------
function Write-Info([string]$msg) { Write-Host "[INFO]  $msg" -ForegroundColor Green }
function Write-WarnMsg([string]$msg) { Write-Host "[WARN]  $msg" -ForegroundColor Yellow }
function Write-ErrMsg([string]$msg) { Write-Host "[ERROR] $msg" -ForegroundColor Red }

function Get-PortListenerPid {
    param([int]$ListenPort)
    try {
        $conns = Get-NetTCPConnection -LocalPort $ListenPort -State Listen -ErrorAction SilentlyContinue
        if ($conns) {
            return @($conns)[0].OwningProcess
        }
    } catch {
        # Get-NetTCPConnection 在极老系统上可能不可用
    }
    return $null
}

function Test-ProcessAlive {
    param([int]$ProcessId)
    if ($ProcessId -le 0) { return $false }
    return $null -ne (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)
}

function Read-PidFile {
    if (-not (Test-Path -LiteralPath $PidFile)) { return $null }
    $raw = (Get-Content -LiteralPath $PidFile -Raw -ErrorAction SilentlyContinue).Trim()
    $id = 0
    if ([int]::TryParse($raw, [ref]$id) -and $id -gt 0) { return $id }
    return $null
}

function Write-PidFile([int]$ProcessId) {
    Set-Content -LiteralPath $PidFile -Value $ProcessId -Encoding ascii
}

function Get-HeadlessFlag {
    if ($env:XHS_HEADLESS) { return $env:XHS_HEADLESS }
    return 'true'
}

function Test-ProjectLocalPath {
    param([string]$PathValue)
    if (-not $PathValue) { return $false }
    try {
        $full = [System.IO.Path]::GetFullPath($PathValue)
    } catch {
        return $false
    }
    $root = $ProjectRoot.TrimEnd('\') + '\'
    $state = $StateHome.TrimEnd('\') + '\'
    return $full.StartsWith($root, [StringComparison]::OrdinalIgnoreCase) -or
        $full.StartsWith($state, [StringComparison]::OrdinalIgnoreCase)
}

function Write-WrapperCmd {
    # 用 .cmd 包一层：固定 cwd / env，stdout/stderr 落盘，计划任务与 start 共用。
    # 不透传会话里临时注入的外部路径（例如 IDE 自带的 node），避免登录自启时挂掉。
    $cookies = if ($env:COOKIES_PATH -and (Test-ProjectLocalPath $env:COOKIES_PATH)) {
        $env:COOKIES_PATH
    } elseif ($env:COOKIES_PATH -and (Test-Path -LiteralPath $env:COOKIES_PATH)) {
        $env:COOKIES_PATH
    } else {
        Join-Path $ProjectRoot 'cookies.json'
    }
    $headless = Get-HeadlessFlag
    $listen = "127.0.0.1:$Port"

    $lines = @(
        '@echo off'
        'setlocal'
        "cd /d `"$ProjectRoot`""
        "set `"COOKIES_PATH=$cookies`""
    )
    # 标量配置：可直接写入
    foreach ($name in @('XHS_RISK_STREAK_LIMIT', 'XHS_FP_SEED', 'XHS_PROXY')) {
        $val = [Environment]::GetEnvironmentVariable($name, 'Process')
        if ($val) {
            $lines += "set `"$name=$val`""
        }
    }
    # 路径类：仅接受项目内 / state 内路径，拒绝会话临时路径
    foreach ($name in @('XHS_CAMOUFOX_BIN', 'PLAYWRIGHT_DRIVER_PATH', 'PLAYWRIGHT_NODEJS_PATH')) {
        $val = [Environment]::GetEnvironmentVariable($name, 'Process')
        if ($val -and (Test-ProjectLocalPath $val)) {
            $lines += "set `"$name=$val`""
        }
    }
    # >> 追加；首启时 daemon start 会截断旧日志再写
    $lines += "`"$Bin`" -headless=$headless -port $listen >> `"$LogFile`" 2>> `"$ErrLogFile`""
    $lines += 'endlocal'

    $utf8NoBom = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllLines($WrapperCmd, $lines, $utf8NoBom)
}

function Start-DetachedService {
    # UseShellExecute=true + Hidden：新进程组，不继承当前控制台/Job，关终端不杀服务。
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $WrapperCmd
    $psi.WorkingDirectory = $ProjectRoot
    $psi.UseShellExecute = $true
    $psi.WindowStyle = [System.Diagnostics.ProcessWindowStyle]::Hidden
    $p = [System.Diagnostics.Process]::Start($psi)
    if (-not $p) {
        throw 'failed to start wrapper process'
    }
    return $p
}

function Wait-ForListen {
    param([int]$Seconds = 8)
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        $pidOnPort = Get-PortListenerPid -ListenPort $Port
        if ($pidOnPort) { return $pidOnPort }
        Start-Sleep -Milliseconds 250
    }
    return $null
}

function Stop-ProcessTreeSafe {
    param([int]$ProcessId)
    if (-not (Test-ProcessAlive -ProcessId $ProcessId)) { return }
    # 先温和结束（Go 侧监听 SIGINT/SIGTERM，Windows 上 CloseMainWindow 未必触达）
    try {
        Stop-Process -Id $ProcessId -Force -ErrorAction Stop
    } catch {
        # ignore
    }
}

function Invoke-CleanupCamoufox {
    # 常驻 Camoufox 退出后偶发残留
    $orphans = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
            Where-Object {
                $_.Name -match 'camoufox|firefox' -and
                $_.CommandLine -match 'camoufox-profile'
            })
    if ($orphans.Count -eq 0) {
        Write-Info 'no orphan camoufox processes'
        return
    }
    Write-Info "reaping $($orphans.Count) camoufox process(es)..."
    foreach ($o in $orphans) {
        try { Stop-Process -Id $o.ProcessId -Force -ErrorAction SilentlyContinue } catch {}
    }
    Start-Sleep -Milliseconds 500
    $left = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
            Where-Object {
                $_.Name -match 'camoufox|firefox' -and
                $_.CommandLine -match 'camoufox-profile'
            }).Count
    Write-Info "remaining camoufox procs: $left"
}

# --- commands --------------------------------------------------------------
function Do-Start {
    $existing = Get-PortListenerPid -ListenPort $Port
    if ($existing) {
        Write-Info "xiaohongshu-mcp already running on :$Port (pid $existing)"
        Write-PidFile $existing
        return 0
    }

    if (-not (Test-Path -LiteralPath $Bin)) {
        Write-ErrMsg "binary not found: $Bin"
        Write-ErrMsg "set XHS_MCP_BIN or build into $ProjectRoot\bin\xhs-mcp.exe"
        return 1
    }

    $cookies = if ($env:COOKIES_PATH) { $env:COOKIES_PATH } else { Join-Path $ProjectRoot 'cookies.json' }
    if (-not (Test-Path -LiteralPath $cookies)) {
        Write-WarnMsg "cookies.json missing ($cookies) - login may be required"
    }

    $headless = Get-HeadlessFlag
    Write-Info "Starting xiaohongshu-mcp on 127.0.0.1:$Port headless=$headless (cwd=$ProjectRoot)"

    # 每次 start 截断日志，避免无限膨胀；err 同步清空
    '' | Set-Content -LiteralPath $LogFile -Encoding utf8
    '' | Set-Content -LiteralPath $ErrLogFile -Encoding utf8

    Write-WrapperCmd
    $null = Start-DetachedService

    $pidOnPort = Wait-ForListen -Seconds 10
    if ($pidOnPort) {
        Write-PidFile $pidOnPort
        Write-Info "xiaohongshu-mcp started (pid $pidOnPort)"
        Write-Info "logs: $LogFile"
        return 0
    }

    Write-ErrMsg "failed to start - see $ErrLogFile / $LogFile"
    if (Test-Path -LiteralPath $ErrLogFile) {
        Get-Content -LiteralPath $ErrLogFile -Tail 30 | ForEach-Object { Write-Host $_ }
    }
    return 1
}

function Do-Stop {
    $stopped = $false
    $fromFile = Read-PidFile
    if ($fromFile -and (Test-ProcessAlive -ProcessId $fromFile)) {
        Stop-ProcessTreeSafe -ProcessId $fromFile
        $stopped = $true
    }
    #  wrapper cmd 可能先于 xhs-mcp 退出，再按端口扫
    $onPort = Get-PortListenerPid -ListenPort $Port
    if ($onPort) {
        Stop-ProcessTreeSafe -ProcessId $onPort
        $stopped = $true
    }
    # 兜底：按映像名
    Get-Process -Name 'xhs-mcp' -ErrorAction SilentlyContinue | ForEach-Object {
        Stop-ProcessTreeSafe -ProcessId $_.Id
        $stopped = $true
    }

    Remove-Item -LiteralPath $PidFile -Force -ErrorAction SilentlyContinue
    if ($stopped) {
        Write-Info 'xiaohongshu-mcp stopped'
    } else {
        Write-Info 'xiaohongshu-mcp was not running'
    }
    Invoke-CleanupCamoufox
    return 0
}

function Do-Status {
    $p = Get-PortListenerPid -ListenPort $Port
    Write-Host "xiaohongshu-mcp  project=$ProjectRoot  state=$StateHome"
    if ($p) {
        Write-Host "  :$Port  RUNNING  pid $p  bin=$Bin" -ForegroundColor Green
    } else {
        Write-Host "  :$Port  STOPPED  start: $($MyInvocation.ScriptName) start" -ForegroundColor Red
    }

    $task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    if ($task) {
        $info = Get-ScheduledTaskInfo -TaskName $TaskName -ErrorAction SilentlyContinue
        Write-Host "  autostart: INSTALLED (task=$TaskName, state=$($task.State), last=$($info.LastTaskResult))"
    } else {
        Write-Host "  autostart: not installed  (install-autostart to run at logon)"
    }

    $cf = @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue |
            Where-Object {
                $_.Name -match 'camoufox|firefox' -and
                $_.CommandLine -match 'camoufox-profile'
            }).Count
    Write-Host "  camoufox procs: $cf  (run cleanup if orphans pile up)"
    return 0
}

function Do-Health {
    $p = Get-PortListenerPid -ListenPort $Port
    if (-not $p) {
        Write-ErrMsg "xiaohongshu-mcp not running (:$Port)"
        return 1
    }
    try {
        $resp = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/health" -UseBasicParsing -TimeoutSec 3
        if ($resp.StatusCode -eq 200) {
            Write-Info "xiaohongshu-mcp healthy (:$Port /health)"
            return 0
        }
    } catch {
        # fall through
    }
    Write-WarnMsg 'listening but /health not OK'
    return 1
}

function Do-Logs {
    if (-not (Test-Path -LiteralPath $LogFile) -and -not (Test-Path -LiteralPath $ErrLogFile)) {
        Write-WarnMsg "no log files under $LogDir"
        return 1
    }
    Write-Info "tailing $LogFile (and err if present); Ctrl+C to stop"
    $targets = @()
    if (Test-Path -LiteralPath $LogFile) { $targets += $LogFile }
    if (Test-Path -LiteralPath $ErrLogFile) { $targets += $ErrLogFile }
    Get-Content -LiteralPath $targets -Wait -Tail 50
    return 0
}

function Do-InstallAutostart {
    if (-not (Test-Path -LiteralPath $Bin)) {
        Write-ErrMsg "binary not found: $Bin"
        return 1
    }
    Write-WrapperCmd

    # 登录时用 cmd 跑 wrapper；Hidden 由任务「隐藏」设置保证
    $action = New-ScheduledTaskAction `
        -Execute 'cmd.exe' `
        -Argument "/c `"$WrapperCmd`"" `
        -WorkingDirectory $ProjectRoot

    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME

    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -StartWhenAvailable `
        -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 1) `
        -MultipleInstances IgnoreNew

    # 当前用户，无需最高权限（仅本机回环服务）
    $principal = New-ScheduledTaskPrincipal `
        -UserId $env:USERNAME `
        -LogonType Interactive `
        -RunLevel Limited

    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -Settings $settings `
        -Principal $principal `
        -Description 'xiaohongshu-mcp HTTP MCP on 127.0.0.1:18060 (project-local daemon)' `
        -Force | Out-Null

    Write-Info "autostart installed: scheduled task '$TaskName' (AtLogOn, user=$env:USERNAME)"
    Write-Info "wrapper: $WrapperCmd"
    Write-Info 'tip: log off/on or reboot to verify; or run: Start-ScheduledTask -TaskName xiaohongshu-mcp'
    return 0
}

function Do-UninstallAutostart {
    $task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    if (-not $task) {
        Write-Info "autostart task '$TaskName' was not installed"
        return 0
    }
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Info "autostart task '$TaskName' removed"
    return 0
}

function Show-Usage {
    @"
Usage: $($MyInvocation.MyCommand.Name) {start|stop|restart|status|health|logs|cleanup|install-autostart|uninstall-autostart}

Project cwd: $ProjectRoot  (required for cookies.json)
Binary:      $Bin
State:       $StateHome
Listen:      127.0.0.1:$Port
Autostart:   scheduled task name = $TaskName

Env (optional):
  XHS_MCP_BIN, XHS_MCP_STATE_HOME, COOKIES_PATH, XHS_HEADLESS
  XHS_CAMOUFOX_BIN, PLAYWRIGHT_DRIVER_PATH, PLAYWRIGHT_NODEJS_PATH
  XHS_FP_SEED, XHS_PROXY, XHS_RISK_STREAK_LIMIT
"@ | Write-Host
    return 0
}

# --- dispatch --------------------------------------------------------------
$exitCode = switch ($Command) {
    'start' { Do-Start }
    'stop' { Do-Stop }
    'restart' { Do-Stop | Out-Null; Start-Sleep -Seconds 1; Do-Start }
    'status' { Do-Status }
    'health' { Do-Health }
    'health-check' { Do-Health }
    'logs' { Do-Logs }
    'cleanup' { Invoke-CleanupCamoufox; 0 }
    'cleanup-camoufox' { Invoke-CleanupCamoufox; 0 }
    'cleanup-rod' { Invoke-CleanupCamoufox; 0 }
    'install-autostart' { Do-InstallAutostart }
    'uninstall-autostart' { Do-UninstallAutostart }
    'help' { Show-Usage }
    default { Show-Usage; 1 }
}

exit $exitCode
