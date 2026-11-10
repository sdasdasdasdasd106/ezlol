# ezlol-ocr (Windows): a long-running helper. ezlol writes "scan" on stdin; the
# helper captures the League game window, runs the built-in Windows OCR
# (Windows.Media.Ocr, Simplified Chinese when installed) and answers with one
# JSON line: {"width":..,"height":..,"lang":"zh-Hans-CN","lines":[{text,conf,x,y,w}]}
# with x/y/w normalised to the game window (0..1, top-left origin).
# Errors are reported as {"error":"..."}. "quit" or end of input exits.

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$utf8 = New-Object System.Text.UTF8Encoding $false
$stdout = New-Object System.IO.StreamWriter ([Console]::OpenStandardOutput()), $utf8
$stdout.AutoFlush = $true
function Emit($obj) { $stdout.WriteLine(($obj | ConvertTo-Json -Compress -Depth 5)) }

try {
    Add-Type -AssemblyName System.Drawing
    Add-Type -AssemblyName System.Windows.Forms
    Add-Type -AssemblyName System.Runtime.WindowsRuntime
    Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class EzlolWin {
    [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
    [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] public static extern IntPtr FindWindow(string cls, string title);
    [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
    [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr h);
}
"@
    # Real pixels, not DPI-scaled ones, so the capture covers the whole window.
    [void][EzlolWin]::SetProcessDPIAware()

    $null = [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType = WindowsRuntime]
    $null = [Windows.Media.Ocr.OcrResult, Windows.Foundation, ContentType = WindowsRuntime]
    $null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics, ContentType = WindowsRuntime]
    $null = [Windows.Graphics.Imaging.SoftwareBitmap, Windows.Graphics, ContentType = WindowsRuntime]
    $null = [Windows.Globalization.Language, Windows.Globalization, ContentType = WindowsRuntime]

    $asTaskGeneric = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
        $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'
    } | Select-Object -First 1
    function Await($op, [Type]$type) {
        $task = $asTaskGeneric.MakeGenericMethod($type).Invoke($null, @($op))
        [void]$task.Wait(-1)
        $task.Result
    }

    # Prefer Simplified Chinese; fall back to the user's profile languages.
    $engine = $null
    foreach ($tag in @('zh-Hans-CN', 'zh-Hans', 'zh-CN')) {
        $lang = New-Object Windows.Globalization.Language $tag
        if ([Windows.Media.Ocr.OcrEngine]::IsLanguageSupported($lang)) {
            $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($lang)
            if ($engine) { break }
        }
    }
    if (-not $engine) { $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages() }
    if (-not $engine) { throw 'Windows has no OCR language installed' }
    $langTag = $engine.RecognizerLanguage.LanguageTag
    $maxDim = [Windows.Media.Ocr.OcrEngine]::MaxImageDimension
} catch {
    Emit @{ error = "init: $($_.Exception.Message)" }
    exit 1
}

function Get-GameRect {
    $h = [EzlolWin]::FindWindow($null, 'League of Legends (TM) Client')
    if ($h -ne [IntPtr]::Zero -and -not [EzlolWin]::IsIconic($h)) {
        $r = New-Object EzlolWin+RECT
        if ([EzlolWin]::GetWindowRect($h, [ref]$r)) {
            $w = $r.Right - $r.Left; $hgt = $r.Bottom - $r.Top
            if ($w -gt 300 -and $hgt -gt 200) { return New-Object System.Drawing.Rectangle $r.Left, $r.Top, $w, $hgt }
        }
    }
    return [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
}

function Scan {
    $full = Get-GameRect
    # Augment cards sit in the middle of the screen: crop to that band (less to
    # read, no chat box), then map positions back to the full window.
    $cx = [int]($full.Width * 0.08); $cy = [int]($full.Height * 0.12)
    $cw = [int]($full.Width * 0.84); $ch = [int]($full.Height * 0.78)
    $bmp = New-Object System.Drawing.Bitmap $cw, $ch
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    try {
        $g.CopyFromScreen($full.X + $cx, $full.Y + $cy, 0, 0, (New-Object System.Drawing.Size $cw, $ch))
    } finally { $g.Dispose() }

    $scale = 1.0
    if ($cw -gt $maxDim -or $ch -gt $maxDim) {
        $scale = [Math]::Min($maxDim / $cw, $maxDim / $ch)
        $small = New-Object System.Drawing.Bitmap $bmp, ([int]($cw * $scale)), ([int]($ch * $scale))
        $bmp.Dispose(); $bmp = $small
    }

    $ms = New-Object System.IO.MemoryStream
    try {
        $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Bmp)
        $bmp.Dispose()
        $ms.Position = 0
        $ras = [System.IO.WindowsRuntimeStreamExtensions]::AsRandomAccessStream($ms)
        $decoder = Await ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($ras)) ([Windows.Graphics.Imaging.BitmapDecoder])
        $sb = Await ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
        $res = Await ($engine.RecognizeAsync($sb)) ([Windows.Media.Ocr.OcrResult])
        $sb.Dispose()
    } finally { $ms.Dispose() }

    $out = New-Object System.Collections.ArrayList
    foreach ($line in $res.Lines) {
        # Windows may put the three card titles on one OCR line; split a line
        # wherever the gap between words is much wider than the text is tall.
        $words = @($line.Words | Sort-Object { $_.BoundingRect.X })
        $segs = New-Object System.Collections.ArrayList
        $cur = New-Object System.Collections.ArrayList
        $prevRight = $null
        foreach ($w in $words) {
            $b = $w.BoundingRect
            if ($null -ne $prevRight -and ($b.X - $prevRight) -gt (2.5 * [Math]::Max(8, $b.Height))) {
                [void]$segs.Add($cur); $cur = New-Object System.Collections.ArrayList
            }
            [void]$cur.Add($w)
            $prevRight = $b.X + $b.Width
        }
        if ($cur.Count -gt 0) { [void]$segs.Add($cur) }
        foreach ($seg in $segs) {
            $minX = [double]::MaxValue; $minY = [double]::MaxValue; $maxX = 0.0; $maxY = 0.0
            $text = ''
            foreach ($w in $seg) {
                $b = $w.BoundingRect
                $minX = [Math]::Min($minX, $b.X); $minY = [Math]::Min($minY, $b.Y)
                $maxX = [Math]::Max($maxX, $b.X + $b.Width); $maxY = [Math]::Max($maxY, $b.Y + $b.Height)
                if ($text -ne '' -and $w.Text -match '^[A-Za-z0-9]' -and $text -match '[A-Za-z0-9]$') { $text += ' ' }
                $text += $w.Text
            }
            $px = $cx + (($minX + $maxX) / 2) / $scale
            $py = $cy + (($minY + $maxY) / 2) / $scale
            [void]$out.Add(@{
                text = $text
                conf = 1.0
                x    = [Math]::Round($px / $full.Width, 4)
                y    = [Math]::Round($py / $full.Height, 4)
                w    = [Math]::Round((($maxX - $minX) / $scale) / $full.Width, 4)
            })
        }
    }
    return @{ width = $full.Width; height = $full.Height; lang = $langTag; lines = $out }
}

Emit @{ ready = $true; lang = $langTag }
while ($true) {
    $cmd = [Console]::In.ReadLine()
    if ($null -eq $cmd -or $cmd -eq 'quit') { break }
    try { Emit (Scan) } catch { Emit @{ error = $_.Exception.Message } }
}
