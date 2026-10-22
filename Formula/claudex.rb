class Claudex < Formula
  desc "Run Claude Code with GPT through a local Codex subscription gateway"
  homepage "https://github.com/marenzo/claudex"
  license "MIT"
  head "https://github.com/marenzo/claudex.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.Version=HEAD"), "./cmd/claudex"
    doc.install "README.md", "CHANGELOG.md", "THIRD_PARTY_NOTICES.md"
    pkgshare.install "examples"
  end

  service do
    run [opt_bin/"claudex", "ctl", "run"]
    keep_alive true
    environment_variables PATH: std_service_path_env
    stop_timeout 40
  end

  test do
    assert_match "claudex HEAD", shell_output("#{bin}/claudex ctl --version")
    config = testpath/"config.json"
    system bin/"claudex", "ctl", "setup", "--config", config, "--no-login"
    settings = JSON.parse(config.read)
    settings["listen"] = "127.0.0.1:#{free_port}"
    config.write(JSON.generate(settings))
    pid = spawn bin/"claudex", "ctl", "run", "--config", config,
                out: testpath/"gateway.out", err: testpath/"gateway.err"
    begin
      sleep 1
      status = JSON.parse(shell_output("#{bin}/claudex ctl status --json --config #{config}"))
      assert_equal "claudex", status["product"]
      assert_equal "foreground", status["mode"]
    ensure
      Process.kill("TERM", pid)
      Process.wait(pid)
    end
  end
end
