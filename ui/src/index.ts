// The bundle's entry. The app imports this file and calls what it default
// exports, handing it the one object a module receives.
import { defineModule } from '@prusa/connect-ui/slots';

import { holdHost } from './host';
import Page from './page.svelte';
import './styles.css';

export default defineModule((host) => {
  // Everything a component of this module needs is built here, from the host
  // and from nothing else. The host arrives before any component is mounted,
  // so it is held rather than threaded down.
  holdHost(host);

  // Every registration is held to `contributes` in module.yaml: a name the
  // manifest does not declare is refused at run time.
  host.register.page('pushover-notifications', Page);
});
