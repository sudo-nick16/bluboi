run: build
	./bluboi

build: main.go
	go build -o bluboi .
