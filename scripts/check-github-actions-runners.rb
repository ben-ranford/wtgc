#!/usr/bin/env ruby
# frozen_string_literal: true

require "yaml"

github_hosted_runners = %w[ubuntu-latest macos-latest windows-latest].freeze
failed = false

Dir.glob(".github/workflows/*.{yml,yaml}").sort.each do |workflow|
  config = YAML.load_file(workflow)
  jobs = config.fetch("jobs", {})

  jobs.each do |job_name, job|
    next if job.key?("uses")

    runs_on = job["runs-on"]
    valid_runner = github_hosted_runners.include?(runs_on)

    if runs_on == "${{ matrix.runner }}"
      matrix_runners = (job.dig("strategy", "matrix", "include") || []).map { |entry| entry["runner"] }.compact
      valid_runner = matrix_runners.sort == github_hosted_runners.sort
    end

    next if valid_runner

    warn "#{workflow}: job #{job_name.inspect} must use a GitHub-hosted runner; found #{runs_on.inspect}"
    failed = true
  end
end

exit(failed ? 1 : 0)
