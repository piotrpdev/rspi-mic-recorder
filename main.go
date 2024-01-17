package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jfreymuth/pulse"
)

const (
	RECORD_AUDIO_SUCCESS int = 0
	RECORD_AUDIO_FAIL    int = 1
	RECORD_AUDIO_STOP    int = 2
)

func setupLogger(filePath string, debugMode bool) (*os.File, error) {
	// ? https://stackoverflow.com/a/13513490/19020549
	f1, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		return nil, fmt.Errorf("error opening log file: %w", err)
	}

	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}

	if debugMode {
		opts.Level = slog.LevelDebug
	}

	mw := io.MultiWriter(os.Stdout, f1)
	logger := slog.New(slog.NewTextHandler(mw, opts))
	slog.SetDefault(logger)

	return f1, nil
}

func main() {
	logPath := flag.String("logPath", "./vaf.log", "Path to log file")
	debugMode := flag.Bool("debug", false, "Enable debug mode")
	flag.Parse()

	slog.Info("Starting rspi-mic-recorder", slog.String("logPath", *logPath), slog.Bool("debugMode", *debugMode))
	slog.Info("Setting up logger", slog.String("logPath", *logPath))

	f1, err := setupLogger(*logPath, *debugMode)
	if err != nil {
		slog.Error(err.Error())
		slog.Error("rspi-mic-recorder failed to start, exitting")
		os.Exit(1)
	}

	defer f1.Close()

	slog.Info("rspi-mic-recorder started successfully", slog.String("logPath", *logPath), slog.Bool("debugMode", *debugMode))

	c, err := pulse.NewClient()
	if err != nil {
		slog.Error(err.Error())
		slog.Error("Failed to create PulseAudio client, exitting...")
		return
	}
	defer c.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup

	for {
		// Will create the file, keep streaming until it receives
		// a channel message, then save the file
		recordAudioChan := make(chan int)
		audioName := time.Now().Format(time.RFC3339)

		// wg.Add(1)
		// go recordAudio(audioName)
		wg.Add(1)
		go func() {
			defer wg.Done()

			slog.Info("Creaing audio file", slog.String("fileName", audioName))
			file := CreateFile(fmt.Sprintf("%s.wav", audioName), 44100, 1)
			stream, err := c.NewRecord(pulse.Float32Writer(file.Write))
			if err != nil {
				slog.Error(err.Error())
				slog.Error("Failed to create record stream, exitting record function...")
				recordAudioChan <- RECORD_AUDIO_SUCCESS
				return
			}

			stream.Start()

			// ! There is usually a couple of seconds of delay before
			// ! the audio starts actualy being recorded
			<-recordAudioChan
			slog.Info("Stopping recording and saving file", slog.String("fileName", audioName))

			stream.Stop()
			file.Close()

			recordAudioChan <- RECORD_AUDIO_SUCCESS
		}()

		dying := false

		// select will block until a case occurs
		select {
		// we're dying
		case <-ctx.Done():
			dying = true
			recordAudioChan <- RECORD_AUDIO_STOP
		case <-time.After(7 * time.Second):
			recordAudioChan <- RECORD_AUDIO_STOP
		}

		// wait until it finishes somehow (not like this probably?)
		<-recordAudioChan

		// no point compressing if we have
		// like a microsecond before death
		if dying {
			slog.Info("dying, breaking...")
			break
		}

		// We don't have to bother waiting,
		// but ffmpeg should still stop
		// since we'll be waiting for ctx
		// in select there. only delete
		// uncompressed file if compressing
		// done to avoid data loss.
		// limit ffmpeg to like 50% cpu
		// max since we don't care how long
		// it takes.
		// wg.Add(1)
		// go compressAudio(audioName)
	}

	wg.Wait()
	slog.Info("Main done")
}
