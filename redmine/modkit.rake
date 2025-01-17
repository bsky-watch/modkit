require 'yaml'
require 'securerandom'

namespace :modkit do
  dev_null = Logger.new("/dev/null")
  Rails.logger = dev_null
  ActiveRecord::Base.logger = dev_null

  task :print_mappings => :environment do
    mappings = {
      "projectId" => Project.where(:name => "Tickets").take.id,
      "priorities" => {
        "low" => IssuePriority.where(:name => "Low").take.id,
        "normal" => IssuePriority.where(:name => "Normal").take.id,
        "high" => IssuePriority.where(:name => "High").take.id,
        "urgent" => IssuePriority.where(:name => "Urgent").take.id,
      },
      "statuses" => {
        "new" => IssueStatus.where(:name => "New").take.id,
        "inProgress" => IssueStatus.where(:name => "In progress").take.id,
        "completed" => IssueStatus.where(:name => "Closed").take.id,
        "applied" => IssueStatus.where(:name => "Applied").take.id,
        "duplicate" => IssueStatus.where(:name => "Duplicate").take.id,
      },
      "ticketTypes" => {
        "ticket" => Tracker.where(:name => "Ticket").take.id,
        "appeal" => Tracker.where(:name => "Appeal").take.id,
        "recordTicket" => Tracker.where(:name => "Record ticket").take.id,
      },
      "fields" => {
        "did" => IssueCustomField.where(:name => "DID").take.id,
        "handle" => IssueCustomField.where(:name => "Handle").take.id,
        "displayName" => IssueCustomField.where(:name => "Display name").take.id,
        "addToLists" => IssueCustomField.where(:name => "Add to lists").take.id,
        "subject" => IssueCustomField.where(:name => "Subject").take.id,
        "labels" => IssueCustomField.where(:name => "Labels").take.id,
      },
      "users" => [{
        "username" => "<login name of the user in Redmine>",
        "dids" => ["did:plc:..."],
      }],
    }

    puts mappings.to_yaml
  end

  task :print_config => :environment do
    config = {
      "$schema" => "../schema/config.yaml",
      "redmineApiKey" => User.where(:login => "modbot").take.api_key,
      "moderationAccount" => {
        "did" => "did:plc:...",
        "password" => "<password>",
      },
      "publicHostname" => "atproto.example.com",
      "ticketIDEncryptionKey" => SecureRandom.hex(8),
      "labelSigningKey" => `openssl ecparam --name secp256k1 --genkey --noout --outform DER | tail --bytes=+8 | head --bytes=32`.unpack("H*").first,
      "enablePerRecordTickets" => false,
      "lists" => {
        "bad1" => {
          "name" => "List name that would be displayed in the UI",
          "uri" => "at://did:plc:.../app.bsky.graph.list/...",
        },
      },
    }

    puts "# yaml-language-server: $schema=../schema/config.yaml"
    puts config.to_yaml
  end
end

