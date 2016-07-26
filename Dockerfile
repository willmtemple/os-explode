FROM openshift/origin-base
LABEL Description="This image is used to watch for changes in a registry explode new images onto a persistent volume" Version="0.1"

ADD ./exploder /

RUN yum install -y ostree

ENTRYPOINT ["/exploder"]
