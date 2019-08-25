package main

import (
	"encoding/json"
	"io/ioutil"
	"log"
	"os"

	proxy "github.com/esote/http-proxy"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: http-proxy config")
	}

	var proxies proxy.Proxies

	data, err := ioutil.ReadFile(os.Args[1])

	if err != nil {
		log.Fatal(err)
	}

	if err = json.Unmarshal(data, &proxies); err != nil {
		log.Fatal(err)
	}

	errs := proxy.Proxy(&proxies)

	for {
		select {
		case err, ok := <-errs:
			if !ok {
				log.Fatal("all servers died")
			}

			log.Println(err)
		}
	}
}
