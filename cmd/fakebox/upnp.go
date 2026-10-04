package main

import (
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

var (
	reCurrentURI = regexp.MustCompile(`(?s)<CurrentURI>(.*?)</CurrentURI>`)
	reMetaData   = regexp.MustCompile(`(?s)<CurrentURIMetaData>(.*?)</CurrentURIMetaData>`)
	reDCTitle    = regexp.MustCompile(`(?s)<dc:title>(.*?)</dc:title>`)
)

const avTransportNS = "urn:schemas-upnp-org:service:AVTransport:1"

// upnpHandler is the renderer's AVTransport control point on :8091, the
// path every STM preset reaches the speaker's audio through.
func upnpHandler(d *Device, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /AVTransport/Control", func(w http.ResponseWriter, r *http.Request) {
		action := r.Header.Get("SOAPACTION")
		action = strings.Trim(action, `"`)
		if i := strings.LastIndex(action, "#"); i >= 0 {
			action = action[i+1:]
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		extra := ""
		switch action {
		case "SetAVTransportURI":
			uri := ""
			if m := reCurrentURI.FindSubmatch(body); m != nil {
				uri = html.UnescapeString(string(m[1]))
			}
			title := ""
			if m := reMetaData.FindSubmatch(body); m != nil {
				if t := reDCTitle.FindStringSubmatch(html.UnescapeString(string(m[1]))); t != nil {
					title = html.UnescapeString(t[1])
				}
			}
			d.SetURI(strings.TrimSpace(uri), strings.TrimSpace(title))
		case "Play":
			d.Play()
		case "Pause":
			d.Pause()
		case "Stop":
			d.Stop()
		case "Seek":
		case "GetTransportInfo":
			extra = fmt.Sprintf("<CurrentTransportState>%s</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus><CurrentSpeed>1</CurrentSpeed>", d.TransportState())
		case "GetPositionInfo":
			extra = fmt.Sprintf("<Track>1</Track><TrackDuration>0:00:00</TrackDuration><TrackURI>%s</TrackURI><RelTime>0:00:00</RelTime><AbsTime>0:00:00</AbsTime>", esc(d.Playing()))
		default:
			logger.Info("upnp: unsupported action", "action", action)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, soapFault(401, "Invalid Action"))
			return
		}
		logger.Debug("upnp", "action", action)
		w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:%sResponse xmlns:u="%s">%s</u:%sResponse></s:Body></s:Envelope>`,
			action, avTransportNS, extra, action)
	})
	return mux
}

func soapFault(code int, desc string) string {
	return fmt.Sprintf(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%d</errorCode><errorDescription>%s</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`, code, desc)
}
