FROM redmine:6.0.2

RUN apt update && apt install -y build-essential
RUN git clone https://github.com/suer/redmine_webhook.git /usr/src/redmine/plugins/redmine_webhook && sed -i.bak -e 's,unloadable,,' /usr/src/redmine/plugins/redmine_webhook/app/models/webhook.rb
# RUN git clone https://github.com/Restream/redmine__select2.git /usr/src/redmine/plugins/redmine__select2 && git clone https://github.com/Restream/redmine_customize.git /usr/src/redmine/plugins/redmine_customize
# RUN git clone https://github.com/annikoff/redmine_plugin_computed_custom_field.git /usr/src/redmine/plugins/computed_custom_field
# RUN git clone https://github.com/alexwais/redmine_workflow_hidden_fields.git /usr/src/redmine/plugins/redmine_workflow_hidden_fields

RUN apt install -y ruby-graphviz && git clone https://github.com/tckz/redmine-wiki_graphviz_plugin.git /usr/src/redmine/plugins/wiki_graphviz_plugin && sed -i.bak -e 's,unloadable,,' /usr/src/redmine/plugins/wiki_graphviz_plugin/app/controllers/wiki_graphviz_controller.rb

COPY patches modkit-patches
RUN for patch in ./modkit-patches/*.diff; do patch -p1 < "${patch}"; done

RUN echo "config.active_job.queue_adapter = :sidekiq" > config/additional_environment.rb && \
  echo "gem 'sidekiq'" >> Gemfile.local
COPY default_data_loader.rb /usr/src/redmine/lib/redmine/default_data/loader.rb
COPY modkit.rake /usr/src/redmine/lib/tasks/modkit.rake
