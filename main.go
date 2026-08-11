package main

import (
	"fmt"
	"net/http"
	"os"
	"io"
	"flag"
	"time"
	"encoding/json"
	"context"
	"bytes"
	"github.com/coder/websocket"
	"golang.org/x/term"
)

const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36"

type Element struct {
	ID string `json:"id"`
	Name string `json:"name"`
	HWID string `json:"hw_id"`
	Model string `json:"model_name"`
	Software string `json:"software_version"`
	SiteID string `json:"site_id"`
	Connected bool `json:"connected"`
	State string `json:"state"`
}


func main() {
	element := flag.String("element", "", "element_id to query")
	timeout := flag.Duration("timeout", 15*time.Second, "HTTP timeout")
	cmd := flag.String("cmd", "", "run a command and exit, returning the command")
	verbose := flag.Bool("v", false, "print request details")
	flag.Parse()

	if *element == "" {
		flag.Usage()
		os.Exit(1)
	}

	token := os.Getenv("SCM_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "SCM_TOKEN not set")
		os.Exit(1)
	}

	url := "https://api.sase.paloaltonetworks.com/sdwan/v3.2/api/elements/" + *element

	if *verbose {
		fmt.Fprintln(os.Stderr, "GET", url)
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bad request:", err)
		os.Exit(1)
	}
	
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUA)

	client := &http.Client{Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read failed:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
		
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "status %s: %s\n", resp.Status, body)
		os.Exit(1)
	}
	
	var el Element
	if err := json.NewDecoder(resp.Body).Decode(&el); err != nil {
		fmt.Fprintln(os.Stderr, "decode failed:", err)
		os.Exit(1)
	}

	fmt.Printf("%s %s %s %s connected=%v\n", el.Name, el.HWID, el.Model, el.Software, el.Connected)

	fmt.Printf("%+v\n", el)

	ctx := context.Background()

	conn, err := dialToolkit(ctx, token, *element)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial failed:", err)
		os.Exit(1)
	}
	defer conn.CloseNow()

	if *cmd != "" {
		out, err := runOnce(ctx, conn, el.Name+"#", *cmd)
		if err != nil {
			fmt.Fprintln(os.Stderr, "command failed:", err)
			os.Exit(1)
		}
		fmt.Println(clean(out, el.Name+"#"))
		return
	}

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "raw mode failed:", err)
		os.Exit(1)
	}
	defer term.Restore(fd, oldState)

	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if i := bytes.IndexByte(buf[:n], 0x1d); i >= 0 {
					if i > 0 {
						conn.Write(ctx, websocket.MessageText, buf[:i])
					}
					conn.CloseNow()
					return
				}
				if werr := conn.Write(ctx, websocket.MessageText, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "\r\nreadfailed:", err)
			break
		}
		if *verbose {
			fmt.Printf("%q\r\n", data)
		} else {
			os.Stdout.Write(data)
		}
	}

}

