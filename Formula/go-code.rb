class GoCode < Formula
  desc "Runtime for operating coding agents"
  homepage "https://github.com/dennisonbertram/go-code"
  head "https://github.com/dennisonbertram/go-code.git", branch: "main"

  depends_on "go" => :build
  depends_on "curl"

  def install
    system "go", "build", "-o", bin/"harnesscli", "./cmd/harnesscli"
    system "go", "build", "-o", bin/"harnessd", "./cmd/harnessd"

    bin.install "scripts/go-code.sh" => "go-code"
    (share/"go-code").install "prompts"
    (share/"go-code").install "catalog"
  end

  test do
    assert_match "go-code", shell_output("#{bin}/go-code --help")
    assert_path_exists share/"go-code/prompts/catalog.yaml"
    assert_path_exists share/"go-code/catalog/models.json"
  end
end
