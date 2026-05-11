package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type FFProbeResult struct {
	Streams []struct {
		CodecName        string `json:"codec_name"`
		SampleRate       string `json:"sample_rate"`
		BitsPerRawSample string `json:"bits_per_raw_sample"`
		BitsPerSample    int    `json:"bits_per_sample"`
		BitRate          string `json:"bit_rate"`
	} `json:"streams"`

	Format struct {
		BitRate  string            `json:"bit_rate"`
		Duration string            `json:"duration"`
		Size     string            `json:"size"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

type TrackInfo struct {
	Path       string `json:"path"`
	Quality    string `json:"quality"`
	Codec      string `json:"codec"`
	SampleRate string `json:"sample_rate"`
	BitDepth   string `json:"bit_depth,omitempty"`
	Bitrate    string `json:"bitrate,omitempty"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
}

type FileResult struct {
	Info TrackInfo
	Err  error
}

type Report struct {
	Root          string         `json:"root"`
	TotalFiles    int            `json:"total_files"`
	FailedFiles   int            `json:"failed_files"`
	QualityStats  map[string]int `json:"quality_stats"`
	ArtistStats   map[string]int `json:"artist_stats,omitempty"`
	AlbumStats    map[string]int `json:"album_stats,omitempty"`
	ScannedTracks []TrackInfo    `json:"tracks,omitempty"`
}

type PauseState struct {
	Paused      atomic.Bool
	Mu          sync.Mutex
	StartedAt   time.Time
	TotalPaused time.Duration
}

const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorWhite   = "\033[37m"
	colorGray    = "\033[90m"
)

var audioExts = map[string]bool{
	".mp3":  true,
	".flac": true,
	".aac":  true,
	".m4a":  true,
	".ogg":  true,
	".opus": true,
	".wav":  true,
	".aiff": true,
	".aif":  true,
	".alac": true,
	".wma":  true,
}

func main() {
	startTime := time.Now()

	threads := flag.Int("threads", runtime.NumCPU(), "number of parallel ffprobe workers")
	jsonOut := flag.String("json", "", "write JSON report to file")
	csvOut := flag.String("csv", "", "write CSV summary to file")
	ignoreArg := flag.String("ignore", "", "comma-separated folder names to ignore")
	followSymlinks := flag.Bool("follow-symlinks", false, "follow symlinked directories")
	byArtist := flag.Bool("by-artist", false, "include artist summary in JSON/CSV")
	byAlbum := flag.Bool("by-album", false, "include album summary in JSON/CSV")
	showProgress := flag.Bool("progress", true, "show progress while scanning")
	saveTracks := flag.Bool("tracks", false, "include per-file details in JSON")
	colorLong := flag.Bool("color", false, "enable colored console output")

	flag.Usage = func() {
		fmt.Println(`audioscan — Audio Library Quality Scanner

audioscan v1.0.0 - Scan your music collecion and get a quality breakdown.

Usage:
  audioscan [options] /path/to/music

Examples:
  audioscan ~/Music
  audioscan -threads 8 ~/Music
  audioscan -json report.json -csv report.csv ~/Music
  audioscan -ignore ".git,Trash,@eaDir" ~/Music
  audioscan -follow-symlinks ~/Music
  audioscan --color ~/Music

Keyboard:
  SPACE    Pause/resume scanning while running
  Ctrl+C   Quit safely

Note:
  Pause stops new files from being processed.
  Any ffprobe jobs already running may finish first.
  Total scanning time excludes paused time.

Console output:
  Directory: /home/user/Music
  FLAC 16bit 44.1kHz: 120 files (45.2%)
  MP3 CBR 128k 44.1kHz: 12 files (4.5%)
  MP3 CBR 320k 44.1kHz: 10 files (3.8%)
  MP3 VBR V0 44.1kHz: 55 files (20.7%)

MP3 classification:
  common exact bitrates -> MP3 CBR 64k/96k/128k/160k/192k/224k/256k/320k
  otherwise:
    230k+  -> MP3 VBR V0
    210k+  -> MP3 VBR V1
    180k+  -> MP3 VBR V2
    160k+  -> MP3 VBR V3
    140k+  -> MP3 VBR V4
    120k+  -> MP3 VBR V5

Options:`)

		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	root := flag.Arg(0)
	ignoreDirs := parseIgnoreDirs(*ignoreArg)

	if _, err := exec.LookPath("ffprobe"); err != nil {
		fmt.Println("Error: ffprobe is not installed or not in PATH.")
		fmt.Println()
		fmt.Println("Install it with:")
		fmt.Println("  Fedora: sudo dnf install ffmpeg")
		fmt.Println("  Debian/Ubuntu: sudo apt install ffmpeg")
		fmt.Println("  Arch: sudo pacman -S ffmpeg")
		os.Exit(1)
	}

	files, err := collectAudioFiles(root, ignoreDirs, *followSymlinks)
	if err != nil {
		fmt.Println("Scan error:", err)
		os.Exit(1)
	}

	totalFound := len(files)
	if totalFound == 0 {
		fmt.Println("No audio files found.")
		return
	}

	pauseState := &PauseState{}

	stopKeyboard := make(chan struct{})
	cleanupKeyboard := setupKeyboardPause(pauseState, stopKeyboard)

	keyboardCleaned := false
	defer func() {
		if !keyboardCleaned {
			cleanupKeyboard()
		}
	}()

	jobs := make(chan string)
	results := make(chan FileResult)

	var processed int64
	var wg sync.WaitGroup

	for i := 0; i < *threads; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for path := range jobs {
				waitWhilePaused(pauseState)

				info, err := analyzeFile(path)
				atomic.AddInt64(&processed, 1)

				results <- FileResult{
					Info: info,
					Err:  err,
				}
			}
		}()
	}

	go func() {
		for _, file := range files {
			waitWhilePaused(pauseState)
			jobs <- file
		}

		close(jobs)
		wg.Wait()
		close(results)
	}()

	doneProgress := make(chan struct{})
	if *showProgress {
		go progressPrinter(totalFound, &processed, pauseState, doneProgress)
	}

	report := Report{
		Root:         root,
		QualityStats: map[string]int{},
		ArtistStats:  map[string]int{},
		AlbumStats:   map[string]int{},
	}

	for result := range results {
		if result.Err != nil {
			report.FailedFiles++
			continue
		}

		report.TotalFiles++
		report.QualityStats[result.Info.Quality]++

		if *byArtist {
			artist := result.Info.Artist
			if artist == "" {
				artist = "Unknown Artist"
			}
			report.ArtistStats[artist]++
		}

		if *byAlbum {
			album := result.Info.Album
			if album == "" {
				album = "Unknown Album"
			}
			report.AlbumStats[album]++
		}

		if *saveTracks {
			report.ScannedTracks = append(report.ScannedTracks, result.Info)
		}
	}

	close(stopKeyboard)

	cleanupKeyboard()
	keyboardCleaned = true

	if *showProgress {
		close(doneProgress)
		fmt.Print("\r\033[K")
	}

	scanDuration := time.Since(startTime) - pauseState.GetTotalPaused()
	printReport(report, *colorLong, scanDuration)

	if *jsonOut != "" {
		if err := writeJSON(*jsonOut, report); err != nil {
			fmt.Println("JSON export error:", err)
		}
	}

	if *csvOut != "" {
		if err := writeCSV(*csvOut, report); err != nil {
			fmt.Println("CSV export error:", err)
		}
	}
}

func (p *PauseState) Toggle() {
	p.Mu.Lock()
	defer p.Mu.Unlock()

	if p.Paused.Load() {
		p.TotalPaused += time.Since(p.StartedAt)
		p.StartedAt = time.Time{}
		p.Paused.Store(false)
	} else {
		p.StartedAt = time.Now()
		p.Paused.Store(true)
	}
}

func (p *PauseState) GetTotalPaused() time.Duration {
	p.Mu.Lock()
	defer p.Mu.Unlock()

	total := p.TotalPaused
	if p.Paused.Load() && !p.StartedAt.IsZero() {
		total += time.Since(p.StartedAt)
	}

	return total
}

func setupKeyboardPause(pauseState *PauseState, stop <-chan struct{}) func() {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return func() {}
	}

	oldState, err := sttyOutput(tty, "-g")
	if err != nil {
		_ = tty.Close()
		return func() {}
	}

	oldState = strings.TrimSpace(oldState)

	if err := sttyRun(tty, "raw", "-echo"); err != nil {
		_ = tty.Close()
		return func() {}
	}

	go func() {
		buf := make([]byte, 1)

		for {
			select {
			case <-stop:
				return
			default:
			}

			n, err := tty.Read(buf)
			if err != nil || n == 0 {
				time.Sleep(50 * time.Millisecond)
				continue
			}

			if buf[0] == ' ' {
				pauseState.Toggle()
			}

			if buf[0] == 3 { // Ctrl+C
				_ = sttyRun(tty, oldState)
				_ = tty.Close()
				fmt.Println()
				os.Exit(130)
			}
		}
	}()

	return func() {
		_ = sttyRun(tty, oldState)
		_ = tty.Close()
	}
}

func sttyOutput(tty *os.File, args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return string(out), nil
}

func sttyRun(tty *os.File, args ...string) error {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty

	return cmd.Run()
}

func waitWhilePaused(pauseState *PauseState) {
	for pauseState.Paused.Load() {
		time.Sleep(150 * time.Millisecond)
	}
}

func collectAudioFiles(root string, ignoreDirs map[string]bool, followSymlinks bool) ([]string, error) {
	var files []string
	visited := map[string]bool{}

	var walk func(string) error

	walk = func(dir string) error {
		realDir := dir

		if followSymlinks {
			resolved, err := filepath.EvalSymlinks(dir)
			if err == nil {
				realDir = resolved
			}

			if visited[realDir] {
				return nil
			}

			visited[realDir] = true
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}

		for _, entry := range entries {
			name := entry.Name()
			path := filepath.Join(dir, name)

			if entry.IsDir() {
				if ignoreDirs[name] {
					continue
				}

				if err := walk(path); err != nil {
					return err
				}

				continue
			}

			if entry.Type()&os.ModeSymlink != 0 {
				if !followSymlinks {
					continue
				}

				info, err := os.Stat(path)
				if err != nil {
					continue
				}

				if info.IsDir() {
					if ignoreDirs[name] {
						continue
					}

					if err := walk(path); err != nil {
						return err
					}

					continue
				}
			}

			ext := strings.ToLower(filepath.Ext(path))
			if audioExts[ext] {
				files = append(files, path)
			}
		}

		return nil
	}

	err := walk(root)
	return files, err
}

func analyzeFile(path string) (TrackInfo, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name,sample_rate,bits_per_raw_sample,bits_per_sample,bit_rate",
		"-show_entries", "format=bit_rate,duration,size:format_tags=artist,album,album_artist",
		"-of", "json",
		path,
	)

	out, err := cmd.Output()
	if err != nil {
		return TrackInfo{}, err
	}

	var result FFProbeResult
	if err := json.Unmarshal(out, &result); err != nil {
		return TrackInfo{}, err
	}

	if len(result.Streams) == 0 {
		return TrackInfo{}, fmt.Errorf("no audio stream")
	}

	stream := result.Streams[0]

	codec := normalizeCodec(stream.CodecName)
	sampleRate := formatSampleRate(stream.SampleRate)
	bitDepth := getBitDepth(stream.BitsPerRawSample, stream.BitsPerSample)

	bitrateKbps := getBitrateKbps(
		stream.BitRate,
		result.Format.BitRate,
		result.Format.Size,
		result.Format.Duration,
	)

	bitrate := ""
	if bitrateKbps > 0 {
		bitrate = fmt.Sprintf("%dk", bitrateKbps)
	}

	var quality string

	if codec == "MP3" {
		mp3Quality := classifyMP3Quality(bitrateKbps)
		if mp3Quality != "" {
			quality = fmt.Sprintf("MP3 %s %s", mp3Quality, sampleRate)
		} else {
			quality = fmt.Sprintf("MP3 %s", sampleRate)
		}
	} else if codec == "FLAC" || codec == "ALAC" || codec == "WAV" || codec == "AIFF" {
		if bitDepth != "" {
			quality = fmt.Sprintf("%s %s %s", codec, bitDepth, sampleRate)
		} else {
			quality = fmt.Sprintf("%s %s", codec, sampleRate)
		}
	} else if bitrate != "" {
		quality = fmt.Sprintf("%s ~%s %s", codec, bitrate, sampleRate)
	} else {
		quality = fmt.Sprintf("%s %s", codec, sampleRate)
	}

	artist := getTag(result.Format.Tags, "artist")
	if artist == "" {
		artist = getTag(result.Format.Tags, "album_artist")
	}

	album := getTag(result.Format.Tags, "album")

	return TrackInfo{
		Path:       path,
		Quality:    quality,
		Codec:      codec,
		SampleRate: sampleRate,
		BitDepth:   bitDepth,
		Bitrate:    bitrate,
		Artist:     artist,
		Album:      album,
	}, nil
}

func normalizeCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "mp3":
		return "MP3"
	case "flac":
		return "FLAC"
	case "aac":
		return "AAC"
	case "alac":
		return "ALAC"
	case "opus":
		return "OPUS"
	case "vorbis":
		return "OGG"
	case "wmav1", "wmav2", "wmapro":
		return "WMA"
	case "pcm_s16le", "pcm_s24le", "pcm_s32le", "pcm_f32le", "pcm_f64le":
		return "WAV"
	default:
		if codec == "" {
			return "UNKNOWN"
		}
		return strings.ToUpper(codec)
	}
}

func classifyMP3Quality(kbps int) string {
	if kbps == 0 {
		return ""
	}

	if cbr := detectCommonCBR(kbps); cbr > 0 {
		return fmt.Sprintf("CBR %dk", cbr)
	}

	switch {
	case kbps >= 230:
		return "VBR V0"
	case kbps >= 210:
		return "VBR V1"
	case kbps >= 180:
		return "VBR V2"
	case kbps >= 160:
		return "VBR V3"
	case kbps >= 140:
		return "VBR V4"
	case kbps >= 120:
		return "VBR V5"
	default:
		return fmt.Sprintf("~%dk", kbps)
	}
}

func detectCommonCBR(kbps int) int {
	common := []int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}

	for _, rate := range common {
		if abs(kbps-rate) <= 2 {
			return rate
		}
	}

	return 0
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func formatSampleRate(sr string) string {
	n, err := strconv.Atoi(sr)
	if err != nil || n == 0 {
		return "unknown kHz"
	}

	if n%1000 == 0 {
		return fmt.Sprintf("%dkHz", n/1000)
	}

	return fmt.Sprintf("%.1fkHz", float64(n)/1000)
}

func getBitDepth(raw string, sample int) string {
	if raw != "" && raw != "N/A" && raw != "0" {
		return raw + "bit"
	}

	if sample > 0 {
		return fmt.Sprintf("%dbit", sample)
	}

	return ""
}

func getBitrateKbps(streamBR, formatBR, sizeStr, durationStr string) int {
	br := parseInt(streamBR)

	if br == 0 {
		br = parseInt(formatBR)
	}

	if br == 0 {
		size := parseFloat(sizeStr)
		duration := parseFloat(durationStr)

		if size > 0 && duration > 0 {
			br = int((size * 8) / duration)
		}
	}

	if br == 0 {
		return 0
	}

	return int(math.Round(float64(br) / 1000.0))
}

func getTag(tags map[string]string, key string) string {
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func parseInt(s string) int {
	if s == "" || s == "N/A" {
		return 0
	}

	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}

	return n
}

func parseFloat(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}

	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}

	return n
}

func parseIgnoreDirs(arg string) map[string]bool {
	ignore := map[string]bool{}

	if arg == "" {
		return ignore
	}

	for _, part := range strings.Split(arg, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			ignore[part] = true
		}
	}

	return ignore
}

func progressPrinter(total int, processed *int64, pauseState *PauseState, done <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			current := atomic.LoadInt64(processed)

			if pauseState.Paused.Load() {
				fmt.Printf("\rScanning: %d/%d [PAUSED]", current, total)
			} else {
				fmt.Printf("\rScanning: %d/%d         ", current, total)
			}
		}
	}
}

func printReport(report Report, useColor bool, scanDuration time.Duration) {
	fmt.Printf("Directory: %s\n\n", report.Root)

	keys := make([]string, 0, len(report.QualityStats))
	for k := range report.QualityStats {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	for _, k := range keys {
		count := report.QualityStats[k]
		percent := 0.0

		if report.TotalFiles > 0 {
			percent = (float64(count) / float64(report.TotalFiles)) * 100
		}

		label := "files"
		if count == 1 {
			label = "file"
		}

		line := fmt.Sprintf("%s: %d %s (%.1f%%)", k, count, label, percent)

		if useColor {
			line = colorizeQualityLine(k, line)
		}

		fmt.Println(line)
	}

	fmt.Println()
	fmt.Printf("Total audio files: %d\n", report.TotalFiles)
	fmt.Printf("Total scanning time: %s\n", formatDuration(scanDuration))
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}

	totalSeconds := int(d.Seconds())

	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}

	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	return fmt.Sprintf("%ds", seconds)
}

func colorizeQualityLine(quality string, line string) string {
	codec := strings.Fields(quality)
	if len(codec) == 0 {
		return line
	}

	switch codec[0] {
	case "FLAC":
		return colorGreen + line + colorReset
	case "MP3":
		return colorYellow + line + colorReset
	case "AAC":
		return colorCyan + line + colorReset
	case "OPUS", "OGG":
		return colorMagenta + line + colorReset
	case "WMA":
		return colorBlue + line + colorReset
	case "WAV", "AIFF":
		return colorRed + line + colorReset
	case "ALAC":
		return colorWhite + line + colorReset
	default:
		return colorGray + line + colorReset
	}
}

func writeJSON(path string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func writeCSV(path string, report Report) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	w := csv.NewWriter(file)
	defer w.Flush()

	if err := w.Write([]string{"type", "name", "files"}); err != nil {
		return err
	}

	writeStats := func(statType string, stats map[string]int) error {
		keys := make([]string, 0, len(stats))
		for k := range stats {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		for _, k := range keys {
			if err := w.Write([]string{
				statType,
				k,
				strconv.Itoa(stats[k]),
			}); err != nil {
				return err
			}
		}

		return nil
	}

	if err := writeStats("quality", report.QualityStats); err != nil {
		return err
	}

	if len(report.ArtistStats) > 0 {
		if err := writeStats("artist", report.ArtistStats); err != nil {
			return err
		}
	}

	if len(report.AlbumStats) > 0 {
		if err := writeStats("album", report.AlbumStats); err != nil {
			return err
		}
	}

	return nil
}
