# Why: the Linux CI runner should come up with one command and survive
# restarts of Docker and of the machine.
#
# What: builds the mk-runner-linux image, registers the runner with the
# repository the first time, and starts it as a container that Docker
# restarts unless stopped.
#
# How: registration runs config.sh in a one-off container with a token from
# `gh api` (the gh login must have admin rights on the repository), so the
# long-running container never holds a token. CPU and memory are capped, and
# the container gets no Docker socket. The image's job-started hook fails
# every job but the nightly workflow of -Repo on main. The mk-runner-linux volume holds the
# runner's install as well as its registration, so after a change of
# RUNNER_VERSION remove the volume (docker volume rm mk-runner-linux) and
# start again to register anew.
param(
    [string]$Repo = "zerodayokay/Mk-Lang",
    [string]$Name = "mk-linux-docker",
    [int]$Cpus = 6,
    [string]$Memory = "16g"
)
$ErrorActionPreference = "Stop"
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$image = "mk-runner-linux"

docker build --build-arg "REPO=$Repo" -t $image $here
if ($LASTEXITCODE -ne 0) { throw "docker build failed" }

if (docker ps -a --filter "name=^$image$" --format "{{.Names}}") {
    docker rm -f $image | Out-Null
}

$volumes = @("-v", "${image}:/home/runner/actions-runner", "-v", "${image}-work:/work")
$registered = docker run --rm @volumes --entrypoint /bin/sh $image -c "test -f /home/runner/actions-runner/.runner && echo yes"
if ($registered -ne "yes") {
    # The token reaches config.sh through ACTIONS_RUNNER_INPUT_TOKEN, which
    # docker copies from this process's environment: it is on no command
    # line.
    $env:ACTIONS_RUNNER_INPUT_TOKEN = gh api -X POST "repos/$Repo/actions/runners/registration-token" --jq .token
    try {
        if (-not $env:ACTIONS_RUNNER_INPUT_TOKEN) { throw "no registration token: is gh logged in with admin rights on $Repo?" }
        docker run --rm @volumes -e ACTIONS_RUNNER_INPUT_TOKEN --entrypoint /bin/bash $image -c `
            "./config.sh --unattended --url https://github.com/$Repo --name $Name --labels mk --work /work --replace"
        if ($LASTEXITCODE -ne 0) { throw "runner registration failed" }
    } finally {
        Remove-Item Env:ACTIONS_RUNNER_INPUT_TOKEN -ErrorAction SilentlyContinue
    }
}

docker run -d --name $image --restart unless-stopped --cpus $Cpus --memory $Memory @volumes $image
if ($LASTEXITCODE -ne 0) { throw "docker run failed" }
Write-Host "Linux runner $Name started; check it under Settings > Actions > Runners."
