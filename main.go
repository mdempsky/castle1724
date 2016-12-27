package main

import (
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"runtime"
	"strconv"

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
		Network: 0xB4,
		Logf:    log.Printf,
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
