package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"

	rconfig "github.com/Luzifer/rconfig/v2"
	"github.com/gofrs/uuid"
	log "github.com/sirupsen/logrus"
)

var (
	cfg = struct {
		Device         string `flag:"input,i" default:"/dev/video0" description:"Video device to read from"`
		FrameRate      int    `flag:"rate,r" default:"10" description:"Frame rate to show in MJPEG"`
		Height         int    `flag:"height,h" default:"720" description:"Height of video frames"`
		Listen         string `flag:"listen" default:":3000" description:"Port/IP to listen on"`
		LogLevel       string `flag:"log-level" default:"info" description:"Log level (debug, info, warn, error, fatal)"`
		Quality        int    `flag:"quality,q" default:"5" description:"Image quality (2..31)"`
		VersionAndExit bool   `flag:"version" default:"false" description:"Prints current version and exits"`
		Width          int    `flag:"width,w" default:"1280" description:"Width of video frames"`
	}{}

	requester     = map[string]chan []byte{}
	requesterLock = new(sync.RWMutex)

	version = "dev"
)

var (
	beginOfJPEG = []byte{0xff, 0xd8}
	endOfJPEG   = []byte{0xff, 0xd9}
)

const maxBacklog = 5

func init() {
	if err := rconfig.ParseAndValidate(&cfg); err != nil {
		log.Fatalf("Unable to parse commandline options: %s", err)
	}

	if cfg.VersionAndExit {
		fmt.Printf("imgdecode %s\n", version)
		os.Exit(0)
	}

	if l, err := log.ParseLevel(cfg.LogLevel); err != nil {
		log.WithError(err).Fatal("Unable to parse log level")
	} else {
		log.SetLevel(l)
	}
}

func main() {
	http.HandleFunc("/mjpeg", handle)
	go func() {
		log.WithError(http.ListenAndServe(cfg.Listen, nil)).Fatal("HTTP server has gone")
	}()

	log.Debug("HTTP server spawned")

	cmd := exec.Command("ffmpeg",
		"-f", "video4linux2",
		"-input_format", "yuyv422",
		"-s", fmt.Sprintf("%dx%d", cfg.Width, cfg.Height),
		"-r", strconv.Itoa(cfg.FrameRate),
		"-i", cfg.Device,
		"-c:v", "mjpeg",
		"-q:v", strconv.Itoa(cfg.Quality),
		"-boundary_tag", "ffmpeg",
		"-f", "image2pipe",
		"-")

	cmd.Stderr = os.Stderr

	out, err := cmd.StdoutPipe()
	if err != nil {
		log.WithError(err).Fatal("Unable to create stdout pipe")
	}

	if err := cmd.Start(); err != nil {
		log.WithError(err).Fatal("Unable to spawn ffmpeg")
	}
	defer cmd.Process.Kill()

	log.Debug("ffmpeg spawned")

	buf := new(bytes.Buffer)

	for {
		if _, err := io.CopyN(buf, out, 1024); err != nil {
			log.WithError(err).Error("Failed to read ffmpeg output")
			break
		}

		eoj := bytes.Index(buf.Bytes(), endOfJPEG)
		if eoj == -1 {
			continue
		}

		img := buf.Next(eoj + len(endOfJPEG))

		if !bytes.HasPrefix(img, beginOfJPEG) || !bytes.HasSuffix(img, endOfJPEG) {
			log.Warn("Found invalid JPEG, skipping")
			continue
		}

		go sendImage(img)
	}
}

func sendImage(jpg []byte) {
	requesterLock.RLock()
	defer requesterLock.RUnlock()

	if len(requester) == 0 {
		return
	}

	for _, c := range requester {
		if len(c) < maxBacklog {
			c <- jpg
		}
	}

	log.WithField("requesters", len(requester)).Debug("sent frame")
}

func handle(res http.ResponseWriter, r *http.Request) {
	imgChan := make(chan []byte, 10)
	uid := uuid.Must(uuid.NewV4()).String()

	defer func() {
		deregisterImgChan(uid)
		close(imgChan)
	}()

	registerImgChan(uid, imgChan)

	handleMJPEG(res, r, imgChan, uid)
}

func registerImgChan(id string, ic chan []byte) {
	requesterLock.Lock()
	defer requesterLock.Unlock()

	requester[id] = ic
	log.WithField("id", id).Debug("registered new requester")
}

func deregisterImgChan(id string) {
	requesterLock.Lock()
	defer requesterLock.Unlock()

	delete(requester, id)
	log.WithField("id", id).Debug("removed requester")
}
