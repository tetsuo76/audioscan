![audioscan](https://github.com/tetsuo76/audioscan/blob/main/screenshot.png?raw=true)

# audioscan

A fast Linux CLI tool for scanning music libraries and reporting audio file formats, quality, and distribution.

`audioscan` recursively scans folders, detects audio file types using `ffprobe`, and groups files by quality such as:

* FLAC 16bit 44.1kHz
* FLAC 24bit 96kHz
* MP3 CBR 320k
* MP3 VBR V0 / V1 / V2
* AAC ~256k
* OPUS ~128k
* WAV / AIFF / ALAC
* WMA / OGG

It is designed for large music collections and helps you quickly understand the overall quality of your library.

---

# Features

* Recursive folder scanning
* Multi-threaded parallel scanning
* Uses `ffprobe` for accurate metadata detection
* MP3 classification:

  * CBR detection (128k / 192k / 320k etc.)
  * VBR detection (V0 / V1 / V2 / V3 / V4 / V5)
* Bit depth detection for lossless formats
* Sample rate detection (44.1kHz / 48kHz / 96kHz etc.)
* Percentage breakdown of total files
* Colored terminal output (`--color`)
* Pause/resume scanning with `SPACE`
* Safe quit with `Ctrl+C`
* Total scanning time (excluding paused time)
* Ignore selected folders
* Follow symlinked directories
* JSON export
* CSV export
* Optional artist/album summaries

---

# Example Output

```text
Directory: /home/user/Music

AAC ~256k 44.1kHz: 12 files (4.5%)
FLAC 16bit 44.1kHz: 320 files (55.2%)
FLAC 24bit 96kHz: 48 files (8.3%)
MP3 CBR 320k 44.1kHz: 15 files (2.6%)
MP3 VBR V0 44.1kHz: 74 files (12.8%)
OPUS ~132k 48kHz: 9 files (1.6%)

Total audio files: 578
Total scanning time: 18s
```

---

# Requirements

## Linux

Currently designed for Linux systems.

## FFmpeg / ffprobe

`audioscan` requires `ffprobe` from FFmpeg.

Install it using:

### Fedora

```bash
sudo dnf install ffmpeg
```

### Debian / Ubuntu

```bash
sudo apt install ffmpeg
```

### Arch Linux

```bash
sudo pacman -S ffmpeg
```

---

# Installation

## Build from source

### Install Go

Requires Go 1.20+ (recommended latest stable)

Check version:

```bash
go version
```

### Clone repository

```bash
git clone https://github.com/yourusername/audioscan.git
cd audioscan
```

### Build

```bash
go build -o audioscan
```

### Run

```bash
./audioscan ~/Music
```

Optional system-wide install:

```bash
sudo cp audioscan /usr/local/bin/
```

Then:

```bash
audioscan ~/Music
```

---

# Usage

```bash
audioscan [options] /path/to/music
```

---

# Examples

## Basic scan

```bash
audioscan ~/Music
```

## Use 8 threads

```bash
audioscan -threads 8 ~/Music
```

## Enable colors

```bash
audioscan --color ~/Music
```

## Export JSON and CSV

```bash
audioscan -json report.json -csv report.csv ~/Music
```

## Ignore folders

```bash
audioscan -ignore ".git,Trash,@eaDir" ~/Music
```

## Follow symlinks

```bash
audioscan -follow-symlinks ~/Music
```

## Include artist/album summaries

```bash
audioscan -by-artist -by-album ~/Music
```

---

# Keyboard Controls

During scanning:

| Key    | Action         |
| ------ | -------------- |
| SPACE  | Pause / Resume |
| Ctrl+C | Quit safely    |

Paused time is excluded from total scanning time.

---

# MP3 Classification

## CBR Detection

Common exact bitrates are classified as:

* 64k
* 96k
* 128k
* 160k
* 192k
* 224k
* 256k
* 320k

Example:

```text
MP3 CBR 320k 44.1kHz
```

## VBR Detection

Approximate VBR mapping:

| Bitrate | Classification |
| ------- | -------------- |
| 230k+   | V0             |
| 210k+   | V1             |
| 180k+   | V2             |
| 160k+   | V3             |
| 140k+   | V4             |
| 120k+   | V5             |

Example:

```text
MP3 VBR V0 44.1kHz
```

---

# JSON Export

Example:

```bash
audioscan -json report.json ~/Music
```

Includes:

* quality statistics
* total files
* failed files
* optional per-file details
* optional artist/album summaries

---

# CSV Export

Example:

```bash
audioscan -csv report.csv ~/Music
```

Useful for spreadsheets and further analysis.

---

# Why This Exists

Large music libraries often contain mixed-quality files from different sources.

`audioscan` helps answer questions like:

* How much of my library is FLAC?
* How many MP3s are still low quality?
* Do I have old 128k files I should replace?
* How much of my collection is true lossless?

without manually checking thousands of files.

---

# License

MIT License
