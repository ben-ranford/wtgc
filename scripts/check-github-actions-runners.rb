#!/usr/bin/env ruby
# frozen_string_literal: true

require "yaml"

expected_runner = "wtgc-arc"
failed = false

Dir.glob(".github/workflows/*.{yml,yaml}").sort.each do |workflow|
  config = YAML.load_file(workflow)
  jobs = config.fetch("jobs", {})

  jobs.each do |job_name, job|
    next if job.key?("uses")

    runs_on = job["runs-on"]
    next if runs_on == expected_runner

    warn "#{workflow}: job #{job_name.inspect} must use runs-on: #{expected_runner.inspect}; found #{runs_on.inspect}"
    failed = true
  end
end

exit(failed ? 1 : 0)
