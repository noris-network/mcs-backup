package app

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"
)

func ezRPC(endpoint, body string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Millisecond}
	reqBody := bytes.NewBufferString(body)
	req, err := http.NewRequest("POST", "http://localhost:"+strconv.Itoa(httpPort)+endpoint, reqBody)
	if err != nil {
		log.Fatalln(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", basicAuth)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	bodyBytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(bodyBytes), nil
}

func ezStream(endpoint, body string) error {
	client := &http.Client{}
	reqBody := bytes.NewBufferString(body)
	req, err := http.NewRequest("POST", "http://localhost:"+strconv.Itoa(httpPort)+endpoint, reqBody)
	if err != nil {
		log.Fatalln(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", basicAuth)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(res.Body)
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Println(line)
	}
	return nil
}
