# Source code server

This is a source code server, which can be used together with Grafana Pyroscope, to use bazel to fetch dependencies.

Note: This is at this moment just at draft stage and shouldn't be used/relied on or be exposed to the public.

## Usage

### Build

```
docker build  -t simonswine/pyroscope-sourcecode-server .

```

### Run

```
# Using docker image
docker run -p 8080:8080 docker.io/simonswine/pyroscope-sourcecode-server:latest

# If you have git and bazel installed
go run ./
```


### Example queries

```
# Query direct source code
curl \
  -d '{"repositoryURL":"https://github.com/bazel-contrib/rules_go","rootPath":"examples/basic_gazelle", "ref":"HEAD","localPath":"printlinks.go"}' \
  -H 'content-type: application/json' \
  -v \
  http://localhost:8080/vcs.v1.VCSService/GetFile

# Query stdlib source code
curl \
  -d '{"repositoryURL":"https://github.com/bazel-contrib/rules_go","rootPath":"examples/basic_gazelle", "ref":"HEAD","localPath":"GOROOT/src/net/http/server.go"}' \
  -H 'content-type: application/json' \
  -v \
  http://localhost:8080/vcs.v1.VCSService/GetFile

# Query a library source code
curl \
  -d '{"repositoryURL":"https://github.com/bazel-contrib/rules_go","rootPath":"examples/basic_gazelle", "ref":"HEAD","localPath":"external/gazelle++go_deps+com_github_stretchr_testify/require/require.go"}' \
  -H 'content-type: application/json' \
  -v \
  http://localhost:8080/vcs.v1.VCSService/GetFile
```
