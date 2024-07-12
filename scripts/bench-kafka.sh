#!/bin/bash

# go get -v github.com/wvanbergen/kafka/tools/stressproducer
go run github.com/wvanbergen/kafka/tools/stressproducer@latest -verbose 2>&1 | head -n100
