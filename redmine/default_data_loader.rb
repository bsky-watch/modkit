# frozen_string_literal: true

# Redmine - project management software
# Copyright (C) 2006-  Jean-Philippe Lang
#
# This program is free software; you can redistribute it and/or
# modify it under the terms of the GNU General Public License
# as published by the Free Software Foundation; either version 2
# of the License, or (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with this program; if not, write to the Free Software
# Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301, USA.

module Redmine
  module DefaultData
    class DataAlreadyLoaded < StandardError; end

    module Loader
      include Redmine::I18n

      class << self
        # Returns true if no data is already loaded in the database
        # otherwise false
        def no_data?
          !Role.where(:builtin => 0).exists? &&
            !Tracker.exists? &&
            !IssueStatus.exists? &&
            !Enumeration.exists? &&
            !Query.exists?
        end

        # Loads the default data
        # Raises a RecordNotSaved exception if something goes wrong
        def load(lang='en', options={})
          raise DataAlreadyLoaded.new("Some configuration data is already loaded.") unless no_data?
          set_language_if_valid(lang)
          workflow = !(options[:workflow] == false)

          Role.transaction do
            # Roles
            admin = Role.create! :name => l(:default_role_admin, default: 'Admin'),
                                   :issues_visibility => 'all',
                                   :users_visibility => 'all',
                                   :position => 1
            admin.permissions = admin.setable_permissions.collect {|p| p.name}
            admin.save!

            moderator =
              Role.create!(
                :name => l(:default_role_moderator, default: 'Moderator'),
                :position => 2,
                :permissions => [
                  :view_issues,
                  :add_issues,
                  :edit_issues,
                  :view_private_notes,
                  :set_notes_private,
                  :manage_issue_relations,
                  :manage_subtasks,
                  :add_issue_notes,
                  :save_queries,
                  :view_gantt,
                  :view_calendar,
                  :log_time,
                  :view_time_entries,
                  :view_news,
                  :comment_news,
                  :view_documents,
                  :view_wiki_pages,
                  :view_wiki_edits,
                  :edit_wiki_pages,
                  :delete_wiki_pages,
                  :view_messages,
                  :add_messages,
                  :view_files,
                  :manage_files,
                  :browse_repository,
                  :view_changesets,
                  :commit_access,
                  :manage_related_issues
                ]
              )
            trainee =
              Role.create!(
                :name => l(:default_role_trainee, default: 'Trainee'),
                :position => 3,
                :permissions => [
                  :view_issues,
                  :add_issues,
                  :add_issue_notes,
                  :save_queries,
                  :view_gantt,
                  :view_calendar,
                  :log_time,
                  :view_time_entries,
                  :view_news,
                  :comment_news,
                  :view_documents,
                  :view_wiki_pages,
                  :view_wiki_edits,
                  :view_messages,
                  :add_messages,
                  :view_files,
                  :browse_repository,
                  :view_changesets
                ]
              )

            automation =
              Role.create!(
                :name => l(:default_role_automation, default: 'Automation'),
                :position => 4,
                :permissions => [
                  :view_issues,
                  :add_issues,
                  :edit_issues,
                  :view_private_notes,
                  :set_notes_private,
                  :manage_issue_relations,
                  :manage_subtasks,
                  :add_issue_notes,
                  :save_queries,
                  :view_messages,
                  :add_messages,
                  :view_files,
                  :manage_files,
                  :manage_related_issues
                ]
              )

            group_admins = Group.create!(:name => 'Admins')
            group_mods = Group.create!(:name => 'Moderators')
            group_trainees = Group.create!(:name => 'Trainees')

            # Issue statuses
            new       = IssueStatus.create!(:name => 'New', :is_closed => false, :position => 1)
            in_progress  = IssueStatus.create!(:name => 'In progress', :is_closed => false, :position => 2)
            closed    = IssueStatus.create!(:name => 'Closed', :is_closed => false, :position => 3)
            rejected  = IssueStatus.create!(:name => 'Rejected', :is_closed => true, :position => 4)
            duplicate  = IssueStatus.create!(:name => 'Duplicate', :is_closed => true, :position => 5)
            applied  = IssueStatus.create!(:name => 'Applied', :is_closed => true, :position => 6)
            granted  = IssueStatus.create!(:name => 'Granted', :is_closed => true, :position => 7)
            denied  = IssueStatus.create!(:name => 'Denied', :is_closed => true, :position => 8)
            invalid  = IssueStatus.create!(:name => 'Invalid', :is_closed => true, :position => 9)

            # Trackers
            ticket = Tracker.create!(:name => 'Ticket', :default_status_id => new.id, :is_in_roadmap => false, :position => 1)
            incident = Tracker.create!(:name => 'Incident', :default_status_id => new.id, :is_in_roadmap => false, :position => 2)
            appeal = Tracker.create!(:name => 'Appeal', :default_status_id => new.id, :is_in_roadmap => false, :position => 3)

            ticket.core_fields = %w(assigned_to_id parent_issue_id description priority_id).freeze
            ticket.save!

            incident.core_fields = %w(assigned_to_id parent_issue_id description priority_id start_date).freeze
            incident.save!

            appeal.core_fields = %w(assigned_to_id parent_issue_id description priority_id).freeze
            appeal.save!



            # Set trackers as defaults for new projects
            Setting.default_projects_tracker_ids = [
              ticket.id.to_s,
              incident.id.to_s,
              appeal.id.to_s,
            ]

            Setting.app_title = 'Modkit'
            Setting.login_required = 1
            Setting.lost_password = 0
            Setting.autologin = 28
            Setting.issue_group_assignment = 1
            Setting.rest_api_enabled = 1
            Setting.attachment_max_size = 51200
            Setting.enabled_scm = []
            Setting.default_projects_modules = %w(issue_tracking news wiki webhooks).freeze

            if workflow
              # Workflow
              Tracker.all.each do |t|
                IssueStatus.all.each do |os|
                  IssueStatus.all.each do |ns|
                    unless os == ns
                      WorkflowTransition.
                        create!(:tracker_id => t.id, :role_id => admin.id,
                                :old_status_id => os.id,
                                :new_status_id => ns.id)
                    end
                  end
                end
              end

              # Tickets
              [new, in_progress, duplicate, invalid].each do |os|
                [in_progress, closed, duplicate, invalid].each do |ns|
                  unless os == ns
                    WorkflowTransition.
                      create!(:tracker_id => ticket.id, :role_id => moderator.id,
                              :old_status_id => os.id,
                              :new_status_id => ns.id)
                  end
                end
              end
              WorkflowTransition.
                create!(:tracker_id => ticket.id, :role_id => moderator.id,
                        :old_status_id => applied.id,
                        :new_status_id => in_progress.id)

              [new].each do |os|
                [in_progress].each do |ns|
                  unless os == ns
                    WorkflowTransition.
                      create!(:tracker_id => ticket.id, :role_id => trainee.id,
                              :old_status_id => os.id, :new_status_id => ns.id)
                  end
                end
              end

              [closed].each do |os|
                [applied, in_progress].each do |ns|
                  unless os == ns
                    WorkflowTransition.
                      create!(:tracker_id => ticket.id, :role_id => automation.id,
                              :old_status_id => os.id, :new_status_id => ns.id)
                  end
                end
              end

              # Incidents
              WorkflowTransition.
                create!(:tracker_id => incident.id, :role_id => moderator.id,
                        :old_status_id => new.id, :new_status_id => in_progress.id)

              [in_progress].each do |os|
                [new, closed].each do |ns|
                  unless os == ns
                    WorkflowTransition.
                      create!(:tracker_id => incident.id, :role_id => moderator.id,
                              :old_status_id => os.id, :new_status_id => ns.id)
                  end
                end
              end

              WorkflowTransition.
                create!(:tracker_id => incident.id, :role_id => moderator.id,
                        :old_status_id => closed.id, :new_status_id => in_progress.id)

              # Appeals
              WorkflowTransition.
                create!(:tracker_id => appeal.id, :role_id => moderator.id,
                        :old_status_id => new.id, :new_status_id => in_progress.id)

              [in_progress].each do |os|
                [new, invalid, granted, denied].each do |ns|
                  unless os == ns
                    WorkflowTransition.
                      create!(:tracker_id => appeal.id, :role_id => moderator.id,
                              :old_status_id => os.id, :new_status_id => ns.id)
                  end
                end
              end

              [invalid, granted, denied].each do |os|
                [in_progress].each do |ns|
                  unless os == ns
                    WorkflowTransition.
                      create!(:tracker_id => appeal.id, :role_id => moderator.id,
                              :old_status_id => os.id, :new_status_id => ns.id)
                  end
                end
              end
            end

            # Enumerations
            IssuePriority.create!(:name => l(:default_priority_low), :position => 1)
            IssuePriority.create!(:name => l(:default_priority_normal), :position => 2, :is_default => true)
            IssuePriority.create!(:name => l(:default_priority_high), :position => 3)
            IssuePriority.create!(:name => l(:default_priority_urgent), :position => 4)

            # Issue queries
            IssueQuery.create!(
              :name => l(:label_assigned_to_me_issues),
              :filters =>
                {
                  'status_id' => {:operator => 'o', :values => ['']},
                  'assigned_to_id' => {:operator => '=', :values => ['me']},
                  'project.status' => {:operator => '=', :values => ['1']}
                },
              :sort_criteria => [['priority', 'desc'], ['updated_on', 'desc']],
              :visibility => Query::VISIBILITY_PUBLIC
            )
            IssueQuery.create!(
              :name => l(:label_reported_issues),
              :filters =>
                {
                  'status_id' => {:operator => 'o', :values => ['']},
                  'author_id' => {:operator => '=', :values => ['me']},
                  'project.status' => {:operator => '=', :values => ['1']}
                },
              :sort_criteria => [['updated_on', 'desc']],
              :visibility => Query::VISIBILITY_PUBLIC
            )
            IssueQuery.create!(
              :name => l(:label_updated_issues),
              :filters =>
                {
                  'status_id' => {:operator => 'o', :values => ['']},
                  'updated_by' => {:operator => '=', :values => ['me']},
                  'project.status' => {:operator => '=', :values => ['1']}
                },
              :sort_criteria => [['updated_on', 'desc']],
              :visibility => Query::VISIBILITY_PUBLIC
            )
            IssueQuery.create!(
              :name => l(:label_watched_issues),
              :filters =>
                {
                  'status_id' => {:operator => 'o', :values => ['']},
                  'watcher_id' => {:operator => '=', :values => ['me']},
                  'project.status' => {:operator => '=', :values => ['1']},
                },
              :sort_criteria => [['updated_on', 'desc']],
              :visibility => Query::VISIBILITY_PUBLIC
            )

            # Project queries
            ProjectQuery.create!(
              :name => l(:label_my_projects),
              :filters =>
                {
                  'status' => {:operator => '=', :values => ['1']},
                  'id' => {:operator => '=', :values => ['mine']}
                },
              :visibility => Query::VISIBILITY_PUBLIC
            )
            ProjectQuery.create!(
              :name => l(:label_my_bookmarks),
              :filters =>
                {
                  'status' => {:operator => '=', :values => ['1']},
                  'id' => {:operator => '=', :values => ['bookmarks']}
                },
              :visibility => Query::VISIBILITY_PUBLIC
            )

            # Custom fields
            common_fields = [
              IssueCustomField.create!(
                :name => "DID",
                :field_format => "string",
                :description => "DID of the subject account",
                :is_filter => true,
                :searchable => true,
                :regexp => "did:.*",
                :url_pattern => "https://bsky.app/profile/%value%",
              ),
              IssueCustomField.create!(
                :name => "Handle",
                :field_format => "string",
                :is_filter => true,
                :searchable => true,
              ),
              IssueCustomField.create!(
                :name => "Display name",
                :field_format => "string",
                :is_filter => true,
                :searchable => true,
              ),
            ]
            ticket_fields = [
              IssueCustomField.create!(
                :name => "Add to lists",
                :field_format => "list",
                :is_filter => true,
                :multiple => true,
                :possible_values => ['dummy'],
              ),
            ]
            ticket.custom_fields << common_fields
            ticket.custom_fields << ticket_fields
            ticket.save!

            appeal.custom_fields << common_fields
            appeal.save!

            # Automation user
            modbot = User.create!(
              :login => "modbot",
              :firstname => "Modbot",
              :lastname => "Automation",
              :mail => "modbot@example.com",
              :admin => true,
              :language => 'en',
              :mail_notification => 'none',
            )

            # Project setup
            project = Project.create!(
              :name => 'Tickets',
              :identifier => 'tickets',
              :is_public => false,
              :issue_custom_fields => common_fields + ticket_fields,
            )

            m = Member.new(
              :project => project,
              :user_id => group_admins.id)
            m.set_editable_role_ids([admin.id])
            m.role_ids = [admin.id]
            project.members << m

            m = Member.new(
              :project => project,
              :user_id => group_mods.id)
            m.set_editable_role_ids([moderator.id])
            m.role_ids = [moderator.id]
            project.members << m

            m = Member.new(
              :project => project,
              :user_id => group_trainees.id)
            m.set_editable_role_ids([trainee.id])
            m.role_ids = [trainee.id]
            project.members << m

            m = Member.new(
              :project => project,
              :user_id => modbot.id)
            m.set_editable_role_ids([automation.id])
            m.role_ids = [automation.id]
            project.members << m

            Webhook.create!(
              :url => 'http://redmine-handler:8080/webhook',
              :project_id => project.id)

            project.save!

            u = User.find(1)
            group_admins.users << u
            group_admins.save!
          end
          true
        end
      end
    end
  end
end
