FROM oraclelinux:7-slim

RUN yum --enablerepo=ol7_optional_latest install -y git golang make

WORKDIR /tmp

ADD . .

RUN make install

FROM oraclelinux:7-slim

RUN yum install -y --enablerepo ol7_developer_EPEL pigz mock && yum clean all

ADD etc /etc

copy --from=0 /usr/bin/smith /usr/bin/smith

VOLUME /write

WORKDIR /write

ENTRYPOINT ["/usr/bin/smith"]
