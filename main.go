package main

import (
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/neghoda/quiet_hn/hn"
)

const cachLifeDuration = 10 * time.Second

type cach struct {
	cashedItems  []item
	expiration   time.Time
	cachMutex    sync.Mutex
	numStories   int
	lifeDuration time.Duration
}

func main() {
	// parse flags
	var port, numStories int
	flag.IntVar(&port, "port", 3000, "the port to start the web server on")
	flag.IntVar(&numStories, "num_stories", 30, "the number of top stories to display")
	flag.Parse()

	tpl := template.Must(template.ParseFiles("./index.gohtml"))

	http.HandleFunc("/", handler(numStories, tpl))

	// Start the server
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func handler(numStories int, tpl *template.Template) http.HandlerFunc {
	c := cach{
		expiration:   time.Now(),
		numStories:   numStories,
		lifeDuration: cachLifeDuration,
	}
	ticker := time.NewTicker(cachLifeDuration / 2)
	go func() {
		for {
			c.updateCach()
			<-ticker.C
		}
	}()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		stories, err := c.getTopStories()
		if err != nil {
			http.Error(w, "Failed to load top stories", http.StatusInternalServerError)
		}
		data := templateData{
			Stories: stories,
			Time:    time.Now().Sub(start),
		}
		err = tpl.Execute(w, data)
		if err != nil {
			http.Error(w, "Failed to process the template", http.StatusInternalServerError)
			return
		}
	})
}

func (c *cach) getTopStories() ([]item, error) {
	if !c.cachExpired() {
		return c.cashedItems, nil
	}
	c.updateCach()
	return c.cashedItems, nil
}

func (c *cach) updateCach() {
	c.cachMutex.Lock()
	defer c.cachMutex.Unlock()
	tempCach, err := fetchTopStories(c.numStories)
	if err != nil {
		return
	}
	c.expiration = time.Now().Add(c.lifeDuration)
	c.cashedItems = tempCach
}

func (c *cach) cachExpired() bool {
	return time.Now().After(c.expiration)
}

func fetchTopStories(numStories int) ([]item, error) {
	var client hn.Client
	ids, err := client.TopItems()
	if err != nil {
		return nil, err
	}
	var stories []item
	type result struct {
		idx   int
		item  item
		error error
	}
	resChan := make(chan result)
	wanted := numStories * 5 / 4
	for i := 0; i < wanted; i++ {
		go func(id int, idx int) {
			hnItem, err := client.GetItem(id)
			if err != nil {
				resChan <- result{error: err}
			}
			resChan <- result{idx: idx, item: parseHNItem(hnItem)}
		}(ids[i], i)
	}
	results := make([]result, 0, numStories)
	for len(results) < numStories {
		res := <-resChan
		if res.error != nil {
			continue
		}
		if isStoryLink(res.item) {
			results = append(results, res)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].idx < results[j].idx
	})
	for _, v := range results {
		stories = append(stories, v.item)
	}
	return stories, nil
}

func isStoryLink(item item) bool {
	return item.Type == "story" && item.URL != ""
}

func parseHNItem(hnItem hn.Item) item {
	ret := item{Item: hnItem}
	url, err := url.Parse(ret.URL)
	if err == nil {
		ret.Host = strings.TrimPrefix(url.Hostname(), "www.")
	}
	return ret
}

// item is the same as the hn.Item, but adds the Host field
type item struct {
	hn.Item
	Host string
}

type templateData struct {
	Stories []item
	Time    time.Duration
}
