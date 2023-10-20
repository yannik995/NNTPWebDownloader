package main

import (
	"fmt"
	"github.com/GJRTimmer/nzb"
	nntpclient "github.com/yannik995/go-nntp/client"
	"github.com/yannik995/yenc"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func maybefatal(s string, e error) {
	if e != nil {
		log.Fatalf("Error in %s: %v", s, e)
	}
}

func main() {
	fileServer := http.FileServer(http.Dir("./static"))
	http.Handle("/static/", http.StripPrefix("/static/", fileServer))

	http.HandleFunc("/", serveTemplate)
	http.HandleFunc("/nzb", nzbHandler)
	http.HandleFunc("/msgids", msgidsHandler)

	fmt.Printf("Starting server at port 8080\n")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal(err)
	}

}

type Main struct {
	Title string
}
type Nzb struct {
	Title string
	Main  string
}

func writeFile(w http.ResponseWriter, name string) {
	hpb, _ := os.ReadFile(filepath.Join("templates", name))
	w.Write(hpb)
}

func serveTemplate(w http.ResponseWriter, r *http.Request) {
	writeFile(w, "header.html")
	writeFile(w, "index.html")
	writeFile(w, "footer.html")
}

func ByteCountSI(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(b)/float64(div), "kMGTPE"[exp])
}

func nzbHandler(w http.ResponseWriter, r *http.Request) {
	//https://stackoverflow.com/a/40699578
	if err := r.ParseMultipartForm(32 << 20); err != nil { //33554432 Bytes Maximal
		_, _ = fmt.Fprintf(w, "ParseMultipartForm() err: %v", err)
		return
	}
	file, header, err := r.FormFile("nzb")
	if err != nil {
		sendHTTPMessage(w, 400, "Bad Request")
		return
	}
	defer file.Close()
	fmt.Printf("File name %s\n", header.Filename)

	fmt.Printf("Parsing NZB...")
	n, err := nzb.Parse(file)
	if err != nil {
		sendHTTPMessage(w, 400, "Bad NZB")
		return
	}

	writeFile(w, "header.html")

	for _, fs := range n.FileSets {

		// Loop Files within the FileSet
		for _, f := range fs.Files {
			// f == *File
			fmt.Fprintf(w, "<p>File: %s (%s)<br/>\r\n", f.Filename, ByteCountSI(int64(f.Size)))
			// Loop over the segments for each file

			fmt.Fprintf(w, "	<form method=\"POST\" action=\"/msgids\">\n"+
				"		<input type=\"hidden\" name=\"msgids\" value=\"")
			for _, s := range f.Segments {
				// s == *Segment
				fmt.Fprintf(w, "%s\n", s.ID)
			}
			fmt.Fprintf(w, "\"/>\n"+
				"		<input type=\"submit\" value=\"Download\"/>\n"+
				"	</form>\r\n")
			fmt.Fprintf(w, "</p>\r\n\r\n")
		}

		// Loop over ParSet Files
		for _, ps := range fs.ParSet.Files {
			// ps == *ParFile
			fmt.Fprintf(w, "<p>ParFile: %s (%s)<br/>\r\n", ps.Filename, ByteCountSI(int64(ps.Size)))
			// Loop over the segments for each file

			fmt.Fprintf(w, "	<form method=\"POST\" action=\"/msgids\">\n"+
				"		<input type=\"hidden\" name=\"msgids\" value=\"")
			for _, s := range ps.Segments {
				// s == *Segment
				fmt.Fprintf(w, "%s\n", s.ID)
			}
			fmt.Fprintf(w, "\"/>\n"+
				"		<input type=\"submit\" value=\"Download\"/>\n"+
				"	</form>\r\n")
			fmt.Fprintf(w, "</p>\r\n\r\n")
		}
	}

	writeFile(w, "footer.html")
}

func msgidsHandler(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		_, _ = fmt.Fprintf(w, "ParseForm() err: %v", err)
		return
	}

	msgids := r.FormValue("msgids")

	downloadFile(w, strings.Split(strings.TrimSuffix(msgids, "\n"), "\n"))

}

func downloadFile(w http.ResponseWriter, msgids []string) {

	c, err := nntpclient.New("tcp", "NEWSREADER:119")
	if err != nil {
		sendHTTPMessage(w, 500, "Failed to connect")
		return
	}
	defer c.Close()

	log.Printf("Got banner:  %v", c.Banner)

	// Authenticate
	msg, err := c.Authenticate("USERNAME", "PASSWORT")
	if err != nil {
		sendHTTPMessage(w, 500, "Failed to connect")
		return
	}
	log.Printf("Post authentication message:  %v", msg)

	var start = true
	var sizePart int64
	var sizeLeft int64
	var part *yenc.Part

	for _, s := range msgids {

		_, id, reader, err := c.Body(fmt.Sprintf("<%s>", strings.TrimSuffix(s, "\r")))

		if err == nil {
			log.Printf("Body of message %v", id)

			part, err = yenc.Decode(reader)
			if err == nil {
				if start {
					if part.Number != 1 {
						sendHTTPMessage(w, 404, "Part 1 missing")
						return
					}

					w.Header().Set("Content-Type", "application/octet-stream")
					w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", part.Name))
					w.Header().Set("Content-Length", strconv.FormatInt(part.HeaderSize, 10))
					w.WriteHeader(200)

					sizePart = part.Size
					sizeLeft = part.HeaderSize
					start = false
				}
				send, err := w.Write(part.Body)
				if err != nil {
					return
				}
				sizeLeft = sizeLeft - int64(send)

				//log.Printf("Size %d Left %d Send %d", sizePart, sizeLeft, send)
				continue
			}
		}
		//Fehler aufgetreten
		if !start {
			send, err := w.Write(make([]byte, int(math.Min(float64(sizePart), float64(sizeLeft)))))
			if err != nil {
				return
			}
			sizeLeft = sizeLeft - int64(send)
		} else {
			sendHTTPMessage(w, 404, "Part 1 missing")
			return
		}

	}
	//log.Printf("Size %d Left %d", sizePart, sizeLeft)
	_, _ = w.Write(make([]byte, sizeLeft))
	if err != nil {
		return
	}

}

func sendHTTPMessage(w http.ResponseWriter, statusCode int, message string) {
	if message == "" {
		http.Error(w, http.StatusText(statusCode), statusCode)
	} else {
		http.Error(w, message, statusCode)
	}
}
