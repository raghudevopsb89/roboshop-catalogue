FROM docker.io/mysql:8.0
COPY schema.sql app-user.sql master-data.sql /db/
COPY run.sh /run.sh
RUN chmod +x /run.sh
ENTRYPOINT ["bash", "/run.sh"]
