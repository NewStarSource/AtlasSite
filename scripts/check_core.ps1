param([switch]$SkipVulnerabilityScan)
$ErrorActionPreference = 'Stop'
$atlas = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$account = [System.IO.Path]::GetFullPath((Join-Path $atlas '../StarAccount'))
function Invoke-Checked([string]$Executable, [string[]]$Arguments) {
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Executable failed ($LASTEXITCODE)" }
}
foreach ($repository in @($atlas, $account)) {
    Push-Location -LiteralPath $repository
    try {
        Invoke-Checked 'go' @('test', './...', '-count=1', '-timeout', '120s')
        Invoke-Checked 'go' @('vet', './...')
        if (-not $SkipVulnerabilityScan) { Invoke-Checked 'go' @('run', 'golang.org/x/vuln/cmd/govulncheck@v1.8.0', './...') }
    } finally { Pop-Location }
}
Push-Location -LiteralPath $atlas
try {
    Invoke-Checked 'go' @('build', '-o', 'bin/staratlas.exe', './cmd/staratlas')
    Invoke-Checked 'go' @('-C', $account, 'build', '-o', 'bin/staraccount.exe', './cmd/staraccount')
    $accountBinary = Join-Path $account 'bin/staraccount.exe'
    $atlasBinary = Join-Path $atlas 'bin/staratlas.exe'
    foreach ($script in @('test_s03.py', 'test_core.py')) {
        Invoke-Checked 'python' @((Join-Path $PSScriptRoot $script), '--account-bin', $accountBinary, '--atlas-bin', $atlasBinary)
    }
} finally { Pop-Location }
Write-Output 'Both services passed Go checks and real HTTP integration. No graphical browser was used.'
