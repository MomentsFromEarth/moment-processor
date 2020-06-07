FROM golang:latest 
RUN mkdir /app 
ADD . /app/ 
WORKDIR /app
RUN go get -u golang.org/x/net/context
RUN go get -u github.com/aws/aws-sdk-go
RUN go get -u google.golang.org/api/youtube/v3
RUN go get -u golang.org/x/oauth2/...
RUN go build -o vid . 
CMD ["/app/vid"]