FROM fedora:24
LABEL Description="This image is used to watch for changes in a registry explode new images onto a persistent volume" Version="0.1"

ADD ./exploder /

RUN dnf install -y ostree

ENTRYPOINT ["/exploder"]
