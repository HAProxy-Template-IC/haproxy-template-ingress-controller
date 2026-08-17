#!/bin/sh
sleep 5
printf 'HTTP/1.1 200 OK\r\nContent-Length: 4\r\nContent-Type: text/plain\r\n\r\nslow'
