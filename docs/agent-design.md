# Hivemind Agent — Design (connexion `direct | agent`)

> Statut : proposition de design. Objectif : permettre à Hivemind de piloter et
> superviser des clusters **distants / derrière NAT** sans exposer leur démon
> Docker, et d'obtenir les **métriques live par nœud** que l'API du manager ne
> donne pas — le tout **sans réécrire le cœur**, en ajoutant l'agent comme un
> simple *mode de transport* derrière les ports existants.

## 1. Objectifs / non-objectifs

**Objectifs**
- Joindre un cluster qui n'accepte **aucune connexion entrante** (l'agent compose vers Hivemind : dial-out / reverse tunnel).
- Couvrir ce que le socket manager ne peut pas : **exec / logs / stats par conteneur sur n'importe quel nœud** + **métriques hôte** (CPU/RAM/disk) de chaque nœud.
- Rester **interchangeable** avec le mode `direct` actuel : même contrat `Orchestrator` (+ futur `MetricsProvider`), résolu par le registry.
- Enrôlement simple : un token collé dans une stack, pas d'échange de certificats par cluster.

**Non-objectifs (pour cette itération)**
- Recalculer les métriques nous-mêmes : on s'appuie sur node-exporter / cAdvisor.
- Stockage temporel central (Thanos/Mimir) : hors scope ; l'agent expose/relaie, il ne stocke pas.
- Support non-Swarm (k8s) de l'agent : le contrat est générique mais l'implémentation cible Swarm.

## 2. Vue d'ensemble

```
                        dial-out (TLS, l'agent compose vers Hivemind)
   ┌─────────────┐   ◄──────────────────────────────────────────┐
   │  Hivemind   │                                               │
   │  (serveur)  │   tunnel multiplexé (yamux/gRPC streams)      │
   │             │   • proxy API Docker (manager)                │
   │  registry ──┼──►• scrape métriques (node-exporter/cAdvisor) │
   │  For(id) →  │   • heartbeat / état                          │
   │  transport  │                                               │
   └─────────────┘                                               │
                                                          ┌──────┴───────┐
                                                          │ agent (svc   │
                                                          │ global Swarm)│
                                                          │ 1 task/nœud  │
                                                          └──────────────┘
                                                          task manager → API Swarm
                                                          task worker  → docker.sock local + exporters
```

- L'agent est déployé en **service global** : une task par nœud.
- La task sur un **manager** relaie l'**API Swarm** (voir/déployer tout le cluster).
- Chaque task lit le **`docker.sock` local** de son nœud (exec/logs/stats locaux) et expose les **exporters** de ce nœud.
- Hivemind ne voit qu'**un endpoint logique** : le tunnel de l'agent.

## 3. Le seam : `ConnectionMode` sur le cluster

Le seul concept nouveau côté domaine.

```go
// internal/domain/cluster
type ConnectionMode string

const (
    ModeDirect ConnectionMode = "direct" // client Docker mTLS sortant (actuel)
    ModeAgent  ConnectionMode = "agent"  // transport via l'agent enrôlé (dial-out)
)
```

Champs ajoutés à `Cluster` :

| champ | sens |
|------|------|
| `ConnectionMode` | `direct` (défaut) \| `agent` |
| `AgentID` | identifiant de l'agent enrôlé (rempli à l'enrôlement) |
| `EnrollmentTokenHash` | hash du token d'enrôlement (le clair n'est jamais stocké) |
| `AgentStatus` | `pending` \| `online` \| `offline` (dérivé des heartbeats) |
| `AgentLastSeen` | timestamp du dernier heartbeat |

Rien d'autre ne change dans le domaine : `Endpoint`/`TLS` restent pour `direct`.

## 4. Résolution dans le registry (cœur inchangé)

Le registry choisit le **transport** ; le reste du code continue d'appeler `Orchestrator`.

```go
func (r *Registry) build(ctx, c *cluster.Cluster) (ports.Orchestrator, error) {
    switch c.ConnectionMode {
    case cluster.ModeDirect:
        return newSwarmFromSpec(ctx, connSpecFrom(c))          // existant (mTLS)
    case cluster.ModeAgent:
        conn, err := r.agentHub.Dial(ctx, c.AgentID)           // tunnel déjà ouvert par l'agent
        if err != nil { return nil, err }
        return newSwarmOverTransport(ctx, conn)                // *même* SwarmOrchestrator,
        //                                                        client Docker monté sur conn
    }
}
```

Point clé : `SwarmOrchestrator` parle au client Docker via un `http.Client`/transport. En mode agent, on lui injecte un **transport monté sur le tunnel** (le client Docker « croit » parler à un démon local). **Zéro changement** dans `DeployService`, `ServiceLogs`, etc.

Idem pour le futur `MetricsProvider` : en mode agent il scrape les exporters **à travers le tunnel**.

## 5. L'« agent hub » côté serveur

Nouveau composant serveur (`internal/adapters/agenthub`) :
- **Écoute les connexions entrantes des agents** (un seul port public TLS).
- À la connexion : **authentifie** (token d'enrôlement → associe l'agent au cluster), établit le **tunnel multiplexé**, garde la session en mémoire.
- Expose `Dial(ctx, agentID) (net.Conn|stream, error)` au registry pour ouvrir un canal logique (API Docker, scrape, exec…) sur la session de l'agent.
- Suit les **heartbeats** → met à jour `AgentStatus`/`AgentLastSeen`.

C'est l'unique surface entrante. Il est **stateful** (sessions ouvertes) mais redémarrable : à la reconnexion l'agent rétablit la session (back-off).

## 6. Enrôlement (token, façon Fleet)

1. Admin : « Ajouter un cluster » → mode **agent**. Hivemind génère un **token d'enrôlement** à usage unique (TTL court), n'en stocke que le **hash**, et renvoie une **commande de déploiement** prête à coller :
   ```bash
   docker stack deploy -c hivemind-agent.yml hivemind-agent
   #   HIVEMIND_SERVER=wss://hivemind.example.com:8443
   #   HIVEMIND_ENROLL_TOKEN=<token-usage-unique>
   ```
2. L'agent démarre, **compose vers `HIVEMIND_SERVER`**, présente le token.
3. Le hub valide le token (non expiré, non consommé), **lie l'agent au cluster** (`AgentID`), émet un **certificat client d'agent** longue durée (rotation possible) → le token est **consommé**.
4. Sessions suivantes : l'agent s'authentifie avec son **certificat**, plus le token.

Le token clair n'existe que le temps de l'enrôlement et n'est **jamais persisté**.

## 7. Ce que l'agent expose dans le tunnel

Trois canaux logiques multiplexés sur une seule connexion sortante :

| canal | usage | implémentation côté agent |
|-------|-------|----------------------------|
| **docker** | API Docker du cluster (orchestration) | proxy vers le `docker.sock` ; sur un nœud manager ⇒ API Swarm complète |
| **node** | exec/logs/stats d'un conteneur + métriques hôte d'**un nœud précis** | route vers la task agent du nœud cible ; lit le `docker.sock` local + exporters |
| **control** | heartbeat, version, enrôlement, mises à jour | messages périodiques (état nœuds, santé agent) |

Routage par nœud : le canal **node** porte un `node_id`; le hub route vers la task agent correspondante (les tasks forment un maillage interne, ou le hub garde une session par task). Cela donne le **runtime par nœud** que le manager seul ne fournit pas.

## 8. Transport

- **Sortant TLS** depuis l'agent (443/8443) → traverse NAT/proxies d'entreprise.
- **Multiplexage** : `yamux` sur une conn TLS, ou **gRPC bidi streams**. Recommandation : gRPC (auth mTLS, streams, keepalive, outillage mûrs) — sinon chisel/yamux si on veut juste tunneler du HTTP brut.
- **Keepalive + back-off** de reconnexion ; le hub marque `offline` après N heartbeats manqués.
- Le client Docker côté serveur est monté sur un `DialContext` custom qui ouvre un stream `docker` → totalement transparent pour `SwarmOrchestrator`.

## 9. Modèle de sécurité (le point sensible)

L'agent a le `docker.sock` ⇒ **équivalent root sur chaque nœud**. Donc :
- **Enrôlement** : token usage unique + TTL court ; jamais stocké en clair (hash seulement).
- **Identité agent** : certificat client émis à l'enrôlement, **rotation** supportée, **révocable** (supprimer le cluster/agent ⇒ révoque).
- **Ingress serveur durci** : un seul port, TLS obligatoire, rate-limiting, refus si token/cert invalide.
- **Moindre privilège** : le proxy Docker de l'agent peut filtrer les appels (all-list d'endpoints) pour ne pas offrir un passe-droit Docker total si non nécessaire.
- **Releases signées** + version épinglée (compat serveur↔agent négociée au handshake).
- **Audit** : chaque action passant par un agent est journalisée avec `cluster_id` + `agent_id` (réutilise l'audit log existant).

## 10. Intégration métriques

En mode agent, le `MetricsProvider` du cluster scrape les exporters **via le canal node** du tunnel — aucun endpoint Prometheus à exposer publiquement. En mode direct, il scrape un Prometheus joignable (cf. design métriques). Même port, deux transports : cohérent avec l'orchestrateur.

## 11. Changements backend (résumé)

- **Domaine** : `ConnectionMode`, `AgentID`, `EnrollmentTokenHash`, `AgentStatus`, `AgentLastSeen` sur `Cluster`.
- **Persistence** : colonnes correspondantes (token hash + cert agent chiffrés via le `Cipher` existant).
- **Ports** : `AgentHub` (driven) — `Dial(ctx, agentID)`, `Sessions()`, signaux de présence.
- **Adapters** : `agenthub` (serveur tunnel) ; `newSwarmOverTransport` (réutilise `SwarmOrchestrator`).
- **Registry** : branche `ModeAgent` (cf. §4) — le reste inchangé.
- **API** :
  - `POST /clusters/:id/enroll` → (ré)génère un token + renvoie le manifeste/commande.
  - `POST /clusters/:id/agent/connect` (ou un endpoint dédié du hub) → handshake agent.
  - `GET /clusters/:id/agent` → état (online/offline, version, nœuds vus).
- **Migration** : `connection_mode` défaut `direct` ⇒ **rétro-compat totale**, les clusters existants ne changent pas.

## 12. Manifeste agent (esquisse, service global Swarm)

```yaml
version: "3.8"
services:
  agent:
    image: hivemind/agent:vX
    deploy:
      mode: global                 # une task par nœud
    environment:
      HIVEMIND_SERVER: ${HIVEMIND_SERVER}
      HIVEMIND_ENROLL_TOKEN: ${HIVEMIND_ENROLL_TOKEN}
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro   # ro quand exec/déploiement non requis sur ce nœud
    # node-exporter / cAdvisor co-déployés (mêmes mode: global) ou embarqués
```

## 13. Modes de défaillance

| panne | comportement |
|-------|--------------|
| Agent perd le réseau | hub marque `offline` ; le registry renvoie `ErrOrchestratorUnavailable` (dégradation gracieuse, déjà géré) |
| Serveur Hivemind redémarre | l'agent se reconnecte (back-off) ; sessions reconstruites |
| Task agent d'un nœud morte | données runtime de ce nœud indisponibles ; orchestration OK via le manager |
| Token fuité | usage unique + TTL ⇒ fenêtre minime ; révocation du cert agent possible |

## 14. Phasage

1. **Seam** : `ConnectionMode` (défaut `direct`) + champs DB + résolution registry no-op pour `agent`. Aucun changement visible.
2. **Hub + handshake** : serveur tunnel, enrôlement par token, heartbeat, état dans l'UI.
3. **Canal docker** : `newSwarmOverTransport` → voir/déployer via l'agent (parité avec `direct`).
4. **Canal node** : exec/logs/stats par nœud + scrape métriques via tunnel.
5. **Durcissement** : rotation cert, révocation, all-list d'endpoints, releases signées.

Chaque phase est livrable indépendamment ; le mode `direct` reste le défaut tant que l'agent n'est pas activé sur un cluster.

## 15. Décisions ouvertes

- Transport : **gRPC bidi** (recommandé) vs yamux/chisel.
- Un agent **par nœud** (service global) vs un agent **manager** + collecte node déléguée aux exporters — le service global est le plus simple et le plus complet.
- Périmètre du proxy Docker : **full passthrough** vs **allow-list** d'endpoints (sécurité vs simplicité).
- Métriques : scrape **à la demande** via tunnel vs **push** périodique de l'agent.
```
