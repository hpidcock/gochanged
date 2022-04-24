# Go Changed :fire:

`gochanged` lists all the packages that have untested changes compared to a base git branch/tag/sha.

## Installing

`go install github.com/hpidcock/gochanged@latest`

## Running

`go test $(gochanged --branch main ./...)`

