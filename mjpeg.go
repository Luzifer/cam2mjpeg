package main

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

func handleMJPEG(res http.ResponseWriter, r *http.Request, imgs chan []byte, uid string) {
	if r.Method != "GET" {
		http.Error(res, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	logger := log.WithField("id", uid)

	mimeWriter := multipart.NewWriter(res)
	mimeWriter.SetBoundary("--boundary")
	defer mimeWriter.Close()

	res.Header().Add("Connection", "close")
	res.Header().Add("Cache-Control", "no-store, no-cache")
	res.Header().Add("Content-Type", fmt.Sprintf("multipart/x-mixed-replace;boundary=%s", mimeWriter.Boundary()))

	cn := res.(http.CloseNotifier).CloseNotify()
	errC := 0

	for {
		select {
		case <-cn:
			return

		case img := <-imgs:
			err := func() error {
				partHeader := make(textproto.MIMEHeader)
				partHeader.Add("Content-Type", "image/jpeg")
				partHeader.Add("Content-Length", strconv.Itoa(len(img)))

				partWriter, err := mimeWriter.CreatePart(partHeader)
				if err != nil {
					return errors.Wrap(err, "Unable to create mime part")
				}

				_, err = partWriter.Write(img)
				return errors.Wrap(err, "Unable to write image")
			}()

			if err != nil {
				logger.WithError(err).Error("Unable to process image")
				errC++

				if errC > 5 {
					logger.Error("Too many errors, killing connection")
					return
				}
				continue
			}

			errC = 0
		}
	}
}
