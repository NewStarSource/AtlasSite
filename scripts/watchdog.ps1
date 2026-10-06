param(
    [string]$AtlasBinary = "$PSScriptRoot/../bin/staratlas.exe",
    [string]$AccountBinary = "$PSScriptRoot/../../StarAccount/bin/staraccount.exe",
    [string]$AtlasConfig = "$PSScriptRoot/../.local/development.json",
    [string]$AccountConfig = "$PSScriptRoot/../../StarAccount/.local/development.json",
    [string]$AlertDirectory = "$PSScriptRoot/../.local/watchdog",
    [switch]$TestAlert
)
$ErrorActionPreference = 'Stop'
$issues = [System.Collections.Generic.List[string]]::new()
foreach ($service in @(
    @{ Name = 'atlas'; Binary = $AtlasBinary; Config = $AtlasConfig; Directory = "$PSScriptRoot/.." },
    @{ Name = 'account'; Binary = $AccountBinary; Config = $AccountConfig; Directory = "$PSScriptRoot/../../StarAccount" }
)) {
    try {
        $configuration = Get-Content -LiteralPath $service.Config -Raw -Encoding UTF8 | ConvertFrom-Json
        $health = Invoke-RestMethod -Uri ($configuration.origin + '/health') -TimeoutSec 5
        if (-not $health.ok) { $issues.Add($service.Name + ':HEALTH_FAILED') }
    } catch { $issues.Add($service.Name + ':HEALTH_UNAVAILABLE') }
    try {
        # Diagnose reads the independent journal, archive hashes and database integrity.
        Push-Location -LiteralPath $service.Directory
        try { $json = & ([System.IO.Path]::GetFullPath($service.Binary)) -config ([System.IO.Path]::GetFullPath($service.Config)) diagnose 2>$null }
        finally { Pop-Location }
        if ($LASTEXITCODE -ne 0) { throw 'diagnostic failed' }
        $state = ($json -join "`n") | ConvertFrom-Json
        if ($state.integrity -ne 'ok') { $issues.Add($service.Name + ':DATABASE_INTEGRITY') }
        if (-not $state.journal_valid) { $issues.Add($service.Name + ':JOURNAL_INVALID') }
        if ($state.backup_stale) { $issues.Add($service.Name + ':BACKUP_STALE') }
        if ($state.invalid_backups -gt 0) { $issues.Add($service.Name + ':BACKUP_INVALID') }
        foreach ($counter in @('failed_jobs', 'overdue_cases', 'queue_backlog')) {
            if ($state.$counter -gt 0) { $issues.Add($service.Name + ':' + $counter.ToUpperInvariant()) }
        }
        if ($state.mode -eq 'isolated') { $issues.Add($service.Name + ':ISOLATED') }
        if ($state.wal_bytes -gt 1GB) { $issues.Add($service.Name + ':WAL_GROWTH') }
        $database = if ([System.IO.Path]::IsPathRooted($configuration.database)) { $configuration.database } else { [System.IO.Path]::GetFullPath((Join-Path $service.Directory $configuration.database)) }
        $drive = [System.IO.DriveInfo]::new([System.IO.Path]::GetPathRoot($database))
        if ($drive.IsReady -and ($drive.AvailableFreeSpace -lt 5GB -or ($drive.AvailableFreeSpace / $drive.TotalSize) -lt 0.2)) {
            $issues.Add($service.Name + ':DISK_LOW')
        }
    } catch { $issues.Add($service.Name + ':DIAGNOSTIC_UNAVAILABLE') }
}
if ($TestAlert) { $issues.Add('synthetic:TEST_ALERT') }
$codes = @($issues | Sort-Object -Unique)
$signature = $codes -join '|'
[System.IO.Directory]::CreateDirectory([System.IO.Path]::GetFullPath($AlertDirectory)) | Out-Null
$stateFile = Join-Path $AlertDirectory 'state.txt'
$previous = if (Test-Path -LiteralPath $stateFile) { Get-Content -LiteralPath $stateFile -Raw -Encoding UTF8 } else { '' }
if ($previous -ne $signature) {
    $alert = @{ time = [DateTime]::UtcNow.ToString('o'); codes = $codes; recovered = ($codes.Count -eq 0) } | ConvertTo-Json -Compress
    Add-Content -LiteralPath (Join-Path $AlertDirectory 'alerts.jsonl') -Value $alert -Encoding UTF8
    [System.IO.File]::WriteAllText($stateFile, $signature, [System.Text.UTF8Encoding]::new($false))
    Write-Output $alert
}
# A scheduler may use this exit code to route an alert through an independently configured channel.
if ($codes.Count -gt 0) { exit 1 }
exit 0
