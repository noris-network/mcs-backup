package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"time"
)

func ezRPC(endpoint, body string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Millisecond}
	reqBody := bytes.NewBufferString(body)
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("http://localhost:%v%v", httpPort, endpoint),
		reqBody,
	)
	if err != nil {
		log.Fatalln(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Authorization", basicAuth)
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	return string(bodyBytes), nil
}

func ezStream(endpoint, body string) error {
	client := &http.Client{}
	reqBody := bytes.NewBufferString(body)
	req, err := http.NewRequest(
		"POST",
		fmt.Sprintf("http://localhost:%v%v", httpPort, endpoint),
		reqBody,
	)
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
	errRe := regexp.MustCompile(`^<<<< ERROR: (.+) >>>>$`)
	for scanner.Scan() {
		line := scanner.Text()
		match := errRe.FindStringSubmatch(line)
		if len(match) == 2 {
			return errors.New(match[1])
		}
		fmt.Println(line)
	}
	return nil
}
