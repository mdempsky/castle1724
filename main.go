package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/julienschmidt/sse"
	"github.com/mdempsky/castle1724/upb"
	"github.com/mdempsky/huejack"
)

type Config struct {
	Devices []Device
}

type Device struct {
	Name     string
	ID       byte
	Dimmable bool
}

const netID = 0xB4

var cfg = Config{
	Devices: []Device{
		{"Family Lights", 1, true},
		{"Family Fan", 2, false},
		{"Kitchen Lights", 3, true},
	},
}

func (c *Config) DeviceNames() []string {
	var res []string
	for i := range c.Devices {
		res = append(res, c.Devices[i].Name)
	}
	return res
}

var conn *upb.Conn

var (
	devFlag  = flag.String("dev", "/dev/cu.usbserial", "serial device file")
	httpFlag = flag.String("http", ":8080", "HTTP service address")
)

func main() {
	flag.Parse()

	var err error
	conn, err = upb.Open(*devFlag, &upb.Config{
		Network: netID,
		Logf:    logf,
		RX:      rxUPB,
	})
	if err != nil {
		log.Fatal(err)
	}

	huejack.Handle(cfg.DeviceNames(), func(key, val int) {
		dev := &cfg.Devices[key]
		fmt.Printf("setting light %v (%q) to %v\n", key, dev.Name, val)
		conn.Goto(dev.ID, byte((val*100+128)/256))
	})
	go huejack.ListenAndServe()

	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/set", setHandler)
	http.HandleFunc("/cmd", cmdHandler)
	http.HandleFunc("/send", sendHandler)
	http.Handle("/log", logStreamer)
	go http.ListenAndServe(*httpFlag, nil)

	log.Println("running")
	runtime.Goexit()
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	index.Execute(w, &cfg)
}

func setHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.FormValue("id"))
	if err != nil {
		log.Println(err)
	} else {
		v, err := strconv.Atoi(r.FormValue("v"))
		if err != nil {
			log.Println(err)
		} else if v < 0 || v > 100 {
			log.Println("value out of range:", v)
		} else {
			err := conn.Goto(byte(id), byte(v))
			if err != nil {
				log.Println(err)
			}
		}
	}
	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

var index = template.Must(template.New("index").Parse(`
<!doctype html>
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>
body { background-color: #4a4a48; }
fieldset, legend { background-color: #f1f2eb; border: thin solid #566246; border-radius: 0.5em; }
legend { padding: 0.2em; }
td a { display: block; background-color: #d8dad3; padding: 0.2em; border-radius: 0.2em; box-shadow: 0.2em 0.2em #4a4a48; color: #4a4a48; text-decoration: none; }
td a:hover { background-color: #a4c2a5; }
td a:active { transform: translate(0.1em, 0.1em); box-shadow: 0.1em 0.1em #4a4a48; }
</style>
<fieldset>
<legend>Devices</legend>
<table>
{{range .Devices}}
<tr>
<th>{{.Name}}
<td><a href="/set?id={{.ID}}&v=0">0%</a></td>
<td>{{if .Dimmable}}<a href="/set?id={{.ID}}&v=25">25%</a>{{end}}
<td>{{if .Dimmable}}<a href="/set?id={{.ID}}&v=50">50%</a>{{end}}
<td>{{if .Dimmable}}<a href="/set?id={{.ID}}&v=75">75%</a>{{end}}
<td><a href="/set?id={{.ID}}&v=100">100%</a>
</tr>
{{end}}
</table>
</fieldset>
`))

func cmdHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	cmdHTML.Execute(w, &cfg)
}

func sendHandler(w http.ResponseWriter, r *http.Request) {
	msg, err := hex.DecodeString(strings.Replace(r.FormValue("msg"), " ", "", -1))
	if err != nil {
		logf("hex decode error: %v", err)
		return
	}
	conn.Send(msg)
}

var cmdHTML = template.Must(template.New("cmd").Parse(`
<!doctype html>
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>
#logframe { width: 100%; height: 30em; font-family: monospace; overflow: scroll; border: thin solid black; }
.examples dt { font-family: monospace; }
</style>

<div>
<p>Provide a UPB message in hex format without checksum. Spaces are ignored.</p>
<p>Examples:</p>
<dl class="examples">
<dt>08 10 b4 01 ff 22 64</dt>
<dd>Turn Family Lights on (100%).</dd>
<dt>08 10 b4 01 ff 22 00</dt>
<dd>Turn Family Lights off (0%).</dd>
</dl>
<p>See <a href="http://www.simply-automated.com/tech_specs/">UPB Tech Specs</a> for protocol and device details.</p>

<label for="cmd">Command:</label>
<input id="cmd" name="cmd" type="text">
<button id="send">Send</button>
</div>

<fieldset id="logframe"><legend>Logs</legend></fieldset>

<script>
var source = new EventSource("/log");
source.onmessage = function (event) {
  var atBottom = logframe.scrollHeight - logframe.clientHeight <= logframe.scrollTop + 1;

  var x = document.createElement('div')
  x.textContent = event.data;
  logframe.appendChild(x);

  if (atBottom) {
    logframe.scrollTop = logframe.scrollHeight - logframe.clientHeight;
  }
};

send.onclick = function() {
  var xhr = new XMLHttpRequest();
  xhr.open("POST", "/send", true);
  xhr.setRequestHeader("Content-type", "application/x-www-form-urlencoded");
  xhr.send("msg=" + encodeURIComponent(cmd.value));

  cmd.value = "";
};
</script>
`))

var logStreamer = sse.New()

func logf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	log.Println(s)

	s = fmt.Sprintf("%v %s", time.Now(), s)
	logStreamer.SendString("", "", s)
}

func rxUPB(msg []byte) {
	go func() {
		switch {
		case msg[0]&0x80 != 0 && msg[2] == netID && msg[3] == 0x0B && msg[5] == 0x20:
			time.Sleep(2 * time.Second)
			conn.Send([]byte{0x07, 0x10, netID, 0x0B, 0xFF, 0x30})
		case msg[0]&0x80 == 0 && msg[2] == netID && msg[3] == 0xFF && msg[4] == 0x0B && msg[5] == 0x86:
			var cmd byte = 0x20
			if msg[6] != 0 {
				cmd = 0x21
			}
			conn.Send([]byte{0x07, 0x10, netID, 0x0B, 0xFF, cmd})
		}
	}()
}
