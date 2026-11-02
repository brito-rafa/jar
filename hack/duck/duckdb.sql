-- CLI getenv reads the Compose token without embedding it in this file.
.output /dev/null
INSTALL quack;
LOAD quack;
INSTALL vss;
LOAD vss;
SET hnsw_enable_experimental_persistence = true;
.serve_command CALL quack_serve('quack:0.0.0.0:9494', allow_other_hostname = true, disable_ssl = true, token = getenv('JAR_DB_TOKEN'));
