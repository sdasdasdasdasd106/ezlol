# ezlol-ocr (Windows): a long-running helper. ezlol sends commands on stdin; the
# helper OCRs the League game window with the built-in Windows OCR
# (Windows.Media.Ocr, Simplified Chinese when installed) and answers with one
# JSON line: {"width":..,"height":..,"lang":"zh-Hans-CN","lines":[{text,conf,x,y,w}]}
# with x/y/w normalised to the game window (0..1, top-left origin).
# Commands (one per line, tab separated):
#   scan[<TAB>shot.png[<TAB>note]]  capture with GDI; optionally save the image
#   file<TAB>img.png<TAB>cx<TAB>cy<TAB>fullW<TAB>fullH<TAB>capture
#        OCR an image ezlol captured itself (Desktop Duplication); cx/cy is its
#        offset inside the game window, fullW x fullH the window size.
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

# Rough black-frame check: exclusive fullscreen can give a capture that is
# all black. Samples a grid of pixels.
function Test-Black($bmp) {
    $sum = 0; $max = 0; $n = 0
    for ($i = 1; $i -lt 20; $i++) {
        for ($j = 1; $j -lt 20; $j++) {
            $c = $bmp.GetPixel([int]($bmp.Width * $i / 20), [int]($bmp.Height * $j / 20))
            $l = ($c.R * 3 + $c.G * 6 + $c.B) / 10
            $sum += $l; if ($l -gt $max) { $max = $l }; $n++
        }
    }
    return (($sum / $n) -lt 6 -and $max -lt 30)
}

# OCR a bitmap that is a crop of the game window: (cx, cy) is the crop's
# offset inside the window, fullW x fullH the window size.
function Read-Bitmap($bmp, [double]$cx, [double]$cy, [double]$fullW, [double]$fullH) {
    $cw = $bmp.Width; $ch = $bmp.Height
    $scale = 1.0
    if ($cw -gt $maxDim -or $ch -gt $maxDim) {
        $scale = [Math]::Min($maxDim / $cw, $maxDim / $ch)
        $small = New-Object System.Drawing.Bitmap $bmp, ([int]($cw * $scale)), ([int]($ch * $scale))
        $bmp = $small
    }
    $ms = New-Object System.IO.MemoryStream
    try {
        $bmp.Save($ms, [System.Drawing.Imaging.ImageFormat]::Bmp)
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
                x    = [Math]::Round($px / $fullW, 4)
                y    = [Math]::Round($py / $fullH, 4)
                w    = [Math]::Round((($maxX - $minX) / $scale) / $fullW, 4)
            })
        }
    }
    return , $out
}

# "scan": capture the game window with GDI (CopyFromScreen) and OCR it.
function Scan([string]$dump, [string]$note) {
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
    if ($dump) {
        try { $bmp.Save($dump, [System.Drawing.Imaging.ImageFormat]::Png) } catch { }
    }
    $capture = 'gdi'
    if ($note) { $capture = "gdi ($note)" }
    if (Test-Black $bmp) {
        $bmp.Dispose()
        return @{ width = $full.Width; height = $full.Height; lang = $langTag; capture = $capture; black = $true; lines = @() }
    }
    $lines = Read-Bitmap $bmp $cx $cy $full.Width $full.Height
    $bmp.Dispose()
    return @{ width = $full.Width; height = $full.Height; lang = $langTag; capture = $capture; lines = $lines }
}

# "file": OCR a frame ezlol already captured (Desktop Duplication) and cropped.
function Scan-File([string]$path, [double]$cx, [double]$cy, [double]$fullW, [double]$fullH, [string]$capture) {
    $bmp = New-Object System.Drawing.Bitmap $path
    try {
        $lines = Read-Bitmap $bmp $cx $cy $fullW $fullH
    } finally { $bmp.Dispose() }
    return @{ width = $fullW; height = $fullH; lang = $langTag; capture = $capture; lines = $lines }
}

Emit @{ ready = $true; lang = $langTag }
while ($true) {
    $cmd = [Console]::In.ReadLine()
    if ($null -eq $cmd -or $cmd -eq 'quit') { break }
    $parts = $cmd.Split("`t")
    try {
        if ($parts[0] -eq 'file' -and $parts.Count -ge 7) {
            Emit (Scan-File $parts[1] ([double]$parts[2]) ([double]$parts[3]) ([double]$parts[4]) ([double]$parts[5]) $parts[6])
        } else {
            $dump = ''; $note = ''
            if ($parts.Count -ge 2) { $dump = $parts[1] }
            if ($parts.Count -ge 3) { $note = $parts[2] }
            Emit (Scan $dump $note)
        }
    } catch { Emit @{ error = $_.Exception.Message } }
}
