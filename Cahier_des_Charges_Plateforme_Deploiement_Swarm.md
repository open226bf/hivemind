**Cahier des Charges**

**Plateforme de Déploiement Autonome pour Docker Swarm**

Version enrichie — Roadmap détaillée MVP → V5

Version du document : 2.0

Date : 11 juin 2026

Auteur : Issa — Document de travail interne, équipe technique

**Table des matières**

# 1. Introduction

## 1.1 Objet du document

Ce document constitue le cahier des charges enrichi de la plateforme de déploiement autonome pour Docker Swarm (nom de code : « Hivemind »). Il décrit le contexte, les objectifs, l’architecture technique cible, le modèle de données, les exigences non fonctionnelles, ainsi que le découpage détaillé des fonctionnalités en six versions : MVP, V1, V2, V3, V4 et V5.

Il s’adresse à l’équipe technique (développeurs backend Go, frontend Angular, DevOps) en charge de la conception et de la réalisation de la plateforme.

## 1.2 Conventions

* Chaque fonctionnalité est identifiée par un code unique de la forme F-<version>-<numéro> (ex. : F-MVP-03, F-V2-01).
* Les priorités suivent la méthode MoSCoW : Must (indispensable), Should (important), Could (souhaitable).
* Les user stories suivent le format « En tant que <rôle>, je veux <action> afin de <bénéfice> ».
* Les endpoints API sont donnés à titre de contrat cible ; ils sont préfixés par /api/v1 en production.

# 2. Contexte et problématique

## 2.1 Situation actuelle

L’entreprise exploite plusieurs applications conteneurisées déployées sur un cluster Docker Swarm. La gestion actuelle repose sur :

* des fichiers docker-stack.yml maintenus manuellement, souvent dupliqués entre projets ;
* des secrets Docker Swarm créés en ligne de commande, sans traçabilité ;
* des configs Docker Swarm versionnées de façon artisanale ;
* des réseaux overlay créés au cas par cas ;
* des webhooks GitLab branchés sur des scripts shell de déploiement.

## 2.2 Limites identifiées

| **Limite** | **Conséquence concrète** |
| --- | --- |
| Complexité croissante des stacks | Les fichiers docker-stack.yml dépassent plusieurs centaines de lignes ; chaque modification est risquée et difficile à relire. |
| Duplication de configurations | Les mêmes blocs (réseaux, logging, ressources) sont copiés-collés entre stacks ; une correction doit être reportée partout. |
| Difficulté de maintenance | Peu de personnes maîtrisent l’ensemble des stacks ; fort « bus factor » sur l’équipe DevOps. |
| Couplage fort entre services d’une même stack | Mettre à jour un seul service force à redéployer ou à manipuler la stack entière. |
| Expertise Swarm obligatoire | Un développeur ne peut pas déployer sans connaître Swarm (secrets, configs, réseaux, contraintes de placement). |
| Absence d’historique fiable | Impossible de répondre simplement à « qui a déployé quoi, quand, et avec quel résultat ? ». |
| Rotation des secrets pénible | Les secrets Swarm étant immuables, leur mise à jour nécessite une procédure manuelle propice aux erreurs. |

## 2.3 Opportunité

L’objectif est de créer une plateforme interne (de type « mini-PaaS ») qui expose un modèle métier simple — service, réseau, secret, configuration, déploiement — et qui traduit ce modèle vers Docker Swarm. Les développeurs déploient en autonomie sans manipuler de stack, tandis que l’équipe DevOps garde le contrôle des règles, des accès et de la supervision.

**Principe fondamental du produit**

*Un dépôt Git correspond à un service autonome. Un service peut être relié librement à des réseaux, secrets et configurations. Le moteur de déploiement traduit ce modèle métier vers Docker Swarm sans exposer la notion de stack à l’utilisateur.*

# 3. Objectifs et indicateurs de succès

## 3.1 Objectifs produit

* Déployer un service indépendamment des autres, sans notion de stack.
* Gérer les réseaux, secrets et configurations du cluster depuis une interface unique.
* Associer librement secrets, configs et réseaux aux services.
* Déclencher automatiquement les déploiements depuis GitLab (webhooks).
* Historiser tous les déploiements (qui, quoi, quand, résultat).
* Superviser l’état des services en temps quasi réel.
* Réduire puis éliminer l’utilisation directe des stacks Docker Swarm.
* Sécuriser les accès par authentification et rôles (Admin, Operator, Viewer).

## 3.2 Indicateurs de succès (KPIs)

| **Indicateur** | **Cible** | **Échéance** |
| --- | --- | --- |
| Délai entre fin de pipeline GitLab et début du rolling update | < 30 secondes | V1 |
| Durée moyenne d’un déploiement complet (lead time) | < 5 minutes | V1 |
| Temps de rollback (MTTR) | < 2 minutes | V1 |
| Part des nouveaux services créés via la plateforme | 100 % | V1 + 6 mois |
| Part des services existants migrés (sans docker-stack.yml) | ≥ 90 % | V2 + 6 mois |
| Taux d’échec des déploiements automatiques | < 5 % | V2 |
| Interventions manuelles DevOps par déploiement standard | 0 | V2 |

# 4. Périmètre

## 4.1 Dans le périmètre (MVP → V5)

* Gestion complète du cycle de vie des services Swarm (création, mise à jour, suppression, scaling).
* Gestion des réseaux, secrets, configurations et volumes.
* Intégration GitLab (dépôts, registry, webhooks, déploiement automatique et manuel).
* Stratégies de déploiement : rolling update, rollback, restart policy, puis blue/green et canary.
* Observabilité : états, logs, métriques, événements, notifications.
* Sécurité : JWT, RBAC, puis SSO/OIDC et tokens d’API.
* Montée en charge : multi-cluster, agents, CLI, GitOps, et à terme Kubernetes.

## 4.2 Hors périmètre

* La construction des images Docker (responsabilité des pipelines GitLab CI).
* L’hébergement et l’administration système des nœuds du cluster (OS, Docker Engine).
* La gestion du code source applicatif et des merge requests.
* Le support d’orchestrateurs autres que Swarm et Kubernetes (Nomad, ECS…).

# 5. Personas et cas d’usage

| **Persona** | **Rôle plateforme** | **Besoins principaux** |
| --- | --- | --- |
| Développeur | Operator ou Viewer | Déployer sa branche, consulter l’état et les logs de son service, faire un rollback sans ticket DevOps. |
| Tech Lead | Operator | Valider les versions déployées, gérer les variables d’environnement et configs de ses services. |
| Ingénieur DevOps / SRE | Admin | Gérer le cluster, les réseaux, les secrets, les accès ; superviser l’ensemble ; définir les stratégies par défaut. |
| Direction technique | Viewer | Vue d’ensemble : quels services tournent, en quelle version, avec quel historique d’incidents. |

## 5.1 Cas d’usage clés

1. Un développeur pousse sur la branche main ; le pipeline GitLab construit l’image ; la plateforme reçoit le webhook et effectue le rolling update automatiquement.
2. Un opérateur déploie manuellement la version v1.0.2 d’un service en pré-production puis la promeut en production.
3. Un déploiement échoue (health check KO) ; la plateforme exécute un rollback automatique et notifie l’équipe.
4. Un admin crée le secret DB\_PASSWORD, l’associe à trois services, puis le fait tourner ; la plateforme redéploie les services concernés.
5. Un admin crée un réseau overlay « monitoring » et y attache des services existants sans interruption.

# 6. Architecture technique

## 6.1 Vue d’ensemble

La plateforme est composée d’un backend Go exposant une API REST, d’une base PostgreSQL pour la persistance du modèle métier et de l’historique, et d’un frontend web Angular. Le backend communique avec le cluster via l’API Docker (socket du manager Swarm dans un premier temps, agents dédiés à partir de la V3) et avec GitLab via son API REST et des webhooks entrants.

## 6.2 Architecture hexagonale (Ports & Adapters)

Le backend suit une architecture hexagonale stricte : le domaine ne dépend d’aucune technologie (ni GORM, ni Gin, ni SDK Docker). Les cas d’usage orchestrent le domaine au travers de ports ; les adapters implémentent ces ports.

| **Couche** | **Contenu** | **Règles de dépendance** |
| --- | --- | --- |
| domain | Entités (Service, Secret, Config, Network, Deployment…), value objects, règles métier, erreurs métier. | Aucune dépendance externe. Go standard uniquement. |
| application | Cas d’usage (DeployService, RotateSecret, AttachNetwork…), transactions, validation métier. | Dépend du domaine et des ports uniquement. |
| ports | Interfaces : driving (ServiceAPI, DeploymentAPI) et driven (ServiceRepository, Orchestrator, SCMProvider, Notifier, Clock). | Interfaces pures, définies côté application/domaine. |
| adapters/persistence | Implémentation des repositories avec GORM/PostgreSQL, migrations. | Implémente les ports driven. |
| adapters/docker | Implémentation du port Orchestrator avec le SDK Docker officiel (client Swarm). | Implémente les ports driven. |
| adapters/gitlab | Client API GitLab v4, validation des webhooks, lecture du registry. | Implémente le port SCMProvider. |
| adapters/api | Handlers HTTP Gin, DTOs, middlewares (auth, RBAC, logging), documentation OpenAPI. | Adapter driving : appelle les cas d’usage. |

**Structure cible du dépôt**

cmd/server/ # point d’entrée API

cmd/migrate/ # migrations base de données

internal/

├── domain/ # entités, value objects, règles métier

├── application/ # cas d’usage (use cases)

├── ports/ # interfaces driving & driven

├── adapters/

│ ├── persistence/ # GORM + PostgreSQL

│ ├── docker/ # SDK Docker / Swarm

│ ├── gitlab/ # API GitLab + webhooks

│ └── api/ # Gin (REST), DTO, middlewares

pkg/ # bibliothèques partagées exportables

## 6.3 Flux de déploiement automatique

1. Le développeur pousse sur la branche configurée du dépôt GitLab.
2. Le pipeline GitLab CI construit l’image Docker et la pousse dans le registry avec un tag (ex. : v1.2.3 ou SHA court).
3. En fin de pipeline, GitLab appelle le webhook de la plateforme (POST signé avec un secret partagé).
4. La plateforme valide la signature, identifie le service lié au dépôt et crée un enregistrement Deployment (statut pending).
5. Le moteur de déploiement traduit le modèle métier (service + réseaux + secrets + configs + stratégie) en appel ServiceUpdate vers l’API Swarm.
6. La plateforme surveille la progression du rolling update (tâches, health checks) et met à jour le statut (running, failed, rolled\_back).
7. Le résultat est historisé et, à partir de la V2, notifié (Slack/email).

## 6.4 Décisions techniques et justifications

| **Choix** | **Justification** |
| --- | --- |
| Go | Performances, binaire unique, écosystème Docker natif (le SDK Docker est en Go), typage fort adapté au domaine. |
| Gin | Framework HTTP léger et éprouvé, middlewares riches, faible empreinte. |
| PostgreSQL + GORM | Fiabilité transactionnelle pour l’historique et l’audit ; GORM accélère le développement (migrations, relations). |
| Architecture hexagonale | Le port Orchestrator isole Swarm : indispensable pour le multi-cluster (V3) et Kubernetes (V5) sans réécriture du domaine. |
| Angular | Framework structurant adapté à une application de gestion ; optionnel au MVP (API-first). |
| JWT (RS256) | Authentification stateless, compatible CLI (V3) et intégrations CI. |
| SDK Docker officiel (github.com/docker/docker/client) | Accès complet à l’API Swarm (services, secrets, configs, networks, tasks, events). |
| OpenAPI (swag) | Documentation générée du contrat API, utilisable par le frontend et la future CLI. |

## 6.5 Sécurité de l’architecture

* Les valeurs des secrets ne sont jamais persistées en clair côté plateforme : elles sont transmises à Swarm puis seules les métadonnées (nom, version, date, empreinte SHA-256) sont conservées. Une valeur de secret n’est jamais relisible via l’API.
* Les tokens GitLab sont chiffrés en base (AES-256-GCM, clé hors base de données).
* Les webhooks entrants sont validés par secret partagé (header X-Gitlab-Token) ; rejet et journalisation des appels invalides.
* TLS obligatoire sur l’API ; communication avec le socket Docker restreinte au manager (puis mTLS via agents en V3).
* Journal d’audit immuable de toutes les actions de mutation (V1).
* Rate limiting et verrouillage de compte après échecs d’authentification répétés.

# 7. Modèle de données (principales entités)

| **Entité** | **Attributs clés** | **Relations** |
| --- | --- | --- |
| User | id, email, password\_hash, role, active, created\_at | 1-N Deployment, 1-N AuditLog |
| Service | id, name (unique), description, image, tag, replicas, command, entrypoint, cpu\_reservation, cpu\_limit, mem\_reservation, mem\_limit, restart\_policy, update\_config (JSON), status | N-N Network, N-N Secret, N-N Config, 1-1 Repository, 1-N Deployment, 1-N EnvVar |
| EnvVar | id, service\_id, key, value (chiffrée si sensible), is\_secret | N-1 Service |
| Network | id, name, driver (overlay), scope, attachable, external (bool), swarm\_id | N-N Service |
| Secret | id, name, current\_version, target\_path, checksum, created\_by, updated\_at | N-N Service, 1-N SecretVersion |
| SecretVersion | id, secret\_id, version, swarm\_secret\_id, checksum, created\_at | N-1 Secret |
| Config | id, name, target\_path, current\_version | N-N Service, 1-N ConfigVersion |
| ConfigVersion | id, config\_id, version, content, swarm\_config\_id, comment, created\_at | N-1 Config |
| Repository | id, service\_id, git\_url, branch, registry\_url, token (chiffré), webhook\_secret | N-1 Service |
| Deployment | id, service\_id, user\_id (nullable si webhook), image\_tag, trigger (manual|webhook|rollback), status, started\_at, finished\_at, error\_message, config\_snapshot (JSON) | N-1 Service, N-1 User |
| AuditLog | id, user\_id, action, resource\_type, resource\_id, payload (JSON), ip, created\_at | N-1 User |

Le champ config\_snapshot de Deployment conserve l’état complet du service au moment du déploiement : il permet le diff entre deux déploiements (V1) et le rollback exact (la plateforme redéploie un snapshot, pas seulement un tag d’image).

# 8. Exigences non fonctionnelles

| **Catégorie** | **Exigence** |
| --- | --- |
| Performance | Traitement d’un webhook < 500 ms (réponse 202, traitement asynchrone). API : p95 < 300 ms hors opérations Swarm. Déclenchement du rolling update < 30 s après réception du webhook. |
| Disponibilité | 99,5 % au MVP (mono-instance), 99,9 % à partir de la V3 (haute disponibilité). L’indisponibilité de la plateforme n’affecte jamais les services déjà déployés. |
| Scalabilité | Jusqu’à 200 services, 50 déploiements/jour et 30 utilisateurs simultanés sur un cluster (MVP→V2) ; multi-cluster à partir de la V3. |
| Sécurité | OWASP ASVS niveau 2 ; secrets non relisibles ; chiffrement at-rest des tokens ; TLS 1.2+ ; audit complet des mutations. |
| Auditabilité | Toute action de mutation tracée (utilisateur, action, ressource, horodatage, IP) ; rétention 24 mois. |
| Maintenabilité | Couverture de tests ≥ 80 % sur domain et application ; tests d’intégration des adapters (Docker-in-Docker, Postgres testcontainers) ; CI GitLab de la plateforme elle-même. |
| Observabilité interne | Logs structurés (JSON), endpoint /healthz et /readyz, métriques Prometheus exposées par la plateforme (/metrics). |
| Compatibilité | Docker Engine ≥ 24.x, API Docker versionnée et négociée ; GitLab ≥ 16 (API v4) ; PostgreSQL ≥ 15. |
| Reprise | Sauvegarde quotidienne de la base ; la reconstruction de l’état plateforme ne doit pas nécessiter d’interroger Swarm (la base fait foi pour le modèle, Swarm pour l’état temps réel). |

# 9. Roadmap — synthèse des versions

Le découpage suit une logique de valeur incrémentale : le MVP livre le moteur de déploiement utilisable via API ; la V1 industrialise (GitLab, UI, RBAC) ; la V2 rend les équipes autonomes (observabilité, migration) ; la V3 passe à l’échelle (multi-cluster, agents, CLI) ; la V4 apporte les déploiements avancés (GitOps, blue/green, canary) ; la V5 ouvre la plateforme au-delà de Swarm (Kubernetes, autoscaling, API publique).

| **Version** | **Thème** | **Contenu clé** | **Durée indicative** | **Jalon de sortie** |
| --- | --- | --- | --- | --- |
| MVP | Le moteur | CRUD services, secrets, configs, réseaux ; moteur de déploiement Swarm ; auth JWT ; API REST documentée. | 3 mois | Premier service de production déployé via l’API, sans docker-stack.yml. |
| V1 | Industrialisation | Intégration GitLab (webhooks, registry), stratégies (rolling update, rollback, restart policy), RBAC 3 rôles, UI Angular, historique enrichi, audit. | 3 mois | Déploiement automatique de bout en bout adopté par une équipe pilote. |
| V2 | Autonomie & observabilité | Logs temps réel, métriques, événements, notifications, volumes, health checks, templates, import des stacks existantes. | 3 mois | ≥ 90 % des services migrés ; 0 intervention DevOps sur un déploiement standard. |
| V3 | Échelle | Multi-cluster, agents sur les nœuds (mTLS), CLI, Traefik natif, certificats TLS, SSO/OIDC, promotion entre environnements, HA plateforme. | 5 mois | Deux clusters pilotés depuis une seule instance HA. |
| V4 | Déploiements avancés | GitOps (réconciliation), blue/green, canary, approbations, monitoring Prometheus/Grafana intégré, alerting, marketplace de templates. | 6 mois | Premier service critique en canary automatisé. |
| V5 | Au-delà de Swarm | Adapter Kubernetes, migration assistée, autoscaling, FinOps, API publique + SDK + Terraform, système de plugins. | 6 mois | Premier service déployé sur Kubernetes via le même modèle métier. |

**Règle de gestion des dépendances :** aucune fonctionnalité d’une version N ne doit nécessiter une réécriture du domaine pour la version N+1. Le port Orchestrator (abstraction de Swarm) et le port SCMProvider (abstraction de GitLab) sont conçus dès le MVP pour absorber le multi-cluster, Kubernetes et d’éventuels autres SCM.

# 10. MVP — « Le moteur » (T0 + 3 mois)

**Objectif :** livrer le cœur de la plateforme : une API REST sécurisée capable de créer, mettre à jour et supprimer des services sur le cluster Swarm, avec gestion des réseaux, secrets et configurations, sans aucune manipulation de docker-stack.yml. Le MVP est volontairement « API-first » : l’interface web complète arrive en V1 ; les premiers utilisateurs (équipe DevOps) consomment l’API via OpenAPI/cURL/Postman.

| **ID** | **Fonctionnalité** | **Priorité** |
| --- | --- | --- |
| F-MVP-01 | Authentification JWT | Must |
| F-MVP-02 | Gestion des services (CRUD) | Must |
| F-MVP-03 | Paramètres d’exécution (CPU/mémoire) | Must |
| F-MVP-04 | Variables d’environnement | Must |
| F-MVP-05 | Gestion des réseaux | Must |
| F-MVP-06 | Gestion des secrets | Must |
| F-MVP-07 | Gestion des configurations | Must |
| F-MVP-08 | Moteur de déploiement Swarm | Must |
| F-MVP-09 | Historique des déploiements (minimal) | Must |
| F-MVP-10 | Supervision de base des services | Should |
| F-MVP-11 | API REST documentée (OpenAPI) | Must |

### F-MVP-01 — Authentification JWT [Must]

Authentification par email/mot de passe délivrant un access token JWT (durée courte) et un refresh token. Au MVP, un seul rôle effectif (Admin) ; le champ rôle est prévu en base pour la V1.

**User stories**

* *En tant qu’ingénieur DevOps, je veux m’authentifier sur l’API afin que seules les personnes autorisées puissent agir sur le cluster.*

**Critères d’acceptation**

* Un utilisateur admin initial est créé au premier démarrage (bootstrap par variable d’environnement).
* Les tokens sont signés en RS256 ; l’access token expire en ≤ 15 min, le refresh token en ≤ 7 jours.
* Toute route hors /auth/\* et /healthz exige un token valide (middleware Gin).
* 5 échecs de connexion consécutifs verrouillent le compte 15 minutes.
* Les mots de passe sont hachés avec bcrypt (coût ≥ 12) ou argon2id.

**API**

* POST /auth/login
* POST /auth/refresh
* POST /auth/logout
* GET /auth/me

### F-MVP-02 — Gestion des services (CRUD) [Must]

Création, consultation, modification et suppression de services. Le service est l’entité centrale du modèle : nom unique, description, image Docker, tag, nombre de réplicas, command et entrypoint personnalisés.

**User stories**

* *En tant qu’opérateur, je veux déclarer un service avec son image et son nombre de réplicas afin de le déployer sans écrire de YAML.*
* *En tant qu’opérateur, je veux modifier le tag d’un service afin de préparer une montée de version.*

**Critères d’acceptation**

* Le nom du service est unique, validé par le pattern ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ (compatible DNS Swarm).
* La création en base ne déploie pas : le déploiement est une action explicite (F-MVP-08) ; le service a un statut (draft, deployed, removed).
* La suppression d’un service déployé exige une confirmation et supprime le service Swarm correspondant.
* Les champs command et entrypoint acceptent une liste d’arguments (tableau JSON), pas une chaîne brute.
* La liste des services est paginée, filtrable par nom et statut.

**API**

* GET /services
* POST /services
* GET /services/{id}
* PUT /services/{id}
* DELETE /services/{id}

### F-MVP-03 — Paramètres d’exécution (CPU / mémoire) [Must]

Définition des réservations et limites de ressources par service, traduites en Resources.Reservations et Resources.Limits de la spécification de service Swarm.

**User stories**

* *En tant qu’ingénieur DevOps, je veux fixer des limites CPU/mémoire afin de protéger le cluster d’un service défaillant.*

**Critères d’acceptation**

* CPU exprimé en cœurs décimaux (ex. : 0.5) et converti en NanoCPUs ; mémoire en unités lisibles (128M, 1G) et convertie en octets.
* Validation : limite ≥ réservation ; valeurs par défaut configurables au niveau plateforme.
* La modification des ressources d’un service déployé déclenche une mise à jour Swarm (rolling update).

### F-MVP-04 — Variables d’environnement [Must]

Gestion des couples KEY=VALUE attachés à un service : ajout, modification, suppression.

**User stories**

* *En tant qu’opérateur, je veux gérer les variables d’environnement de mon service afin d’adapter sa configuration sans reconstruire l’image.*

**Critères d’acceptation**

* KEY validée par ^[A-Z\_][A-Z0-9\_]\*$ ; unicité de la clé par service.
* Les variables marquées sensibles (is\_secret) sont chiffrées en base et masquées dans les réponses API (valeur remplacée par « \*\*\*\*\* »).
* Les variables sont injectées dans la spécification Swarm au déploiement ; un avertissement recommande l’usage des secrets (F-MVP-06) pour les mots de passe.

**API**

* GET /services/{id}/env
* PUT /services/{id}/env (remplacement atomique de la liste)

### F-MVP-05 — Gestion des réseaux [Must]

Visualisation des réseaux du cluster (nom, driver, scope), création de réseaux overlay attachables, suppression, et association/dissociation aux services (ex. : backend, frontend, monitoring).

**User stories**

* *En tant qu’ingénieur DevOps, je veux créer un réseau overlay « monitoring » afin d’y connecter les services d’observabilité.*
* *En tant qu’opérateur, je veux attacher mon service au réseau « backend » afin qu’il communique avec la base de données.*

**Critères d’acceptation**

* La liste fusionne l’état Swarm (source de vérité temps réel) et les métadonnées plateforme ; les réseaux créés hors plateforme apparaissent marqués « external ».
* Seuls les réseaux overlay sont créés par la plateforme ; option attachable activée par défaut.
* La suppression est refusée (409) si des services y sont encore attachés.
* L’association réseau-service est appliquée au prochain déploiement du service, avec mention explicite dans la réponse.

**API**

* GET /networks
* POST /networks
* DELETE /networks/{id}
* POST /services/{id}/networks/{networkId}
* DELETE /services/{id}/networks/{networkId}

### F-MVP-06 — Gestion des secrets [Must]

Création, rotation, suppression de secrets (DB\_PASSWORD, JWT\_SECRET, SMTP\_PASSWORD…) et association aux services avec chemin de montage cible. Les secrets Swarm étant immuables, la « mise à jour » crée une nouvelle version Swarm puis bascule les services associés.

**User stories**

* *En tant qu’ingénieur DevOps, je veux créer un secret et l’associer à plusieurs services afin de centraliser la gestion des credentials.*
* *En tant qu’ingénieur DevOps, je veux faire tourner un secret afin de respecter la politique de rotation, sans procédure manuelle.*

**Critères d’acceptation**

* La valeur d’un secret n’est jamais relisible via l’API ; seules les métadonnées (nom, version, empreinte SHA-256, date) sont exposées.
* La rotation crée un secret Swarm versionné (ex. : db\_password\_v2), met à jour les services associés (rolling update) puis supprime l’ancienne version une fois la bascule réussie.
* La suppression est refusée (409) si le secret est associé à au moins un service.
* L’association précise le chemin cible dans le conteneur (défaut : /run/secrets/<nom>).
* Chaque création/rotation/suppression est historisée (qui, quand).

**API**

* GET /secrets
* POST /secrets
* PUT /secrets/{id} (rotation)
* DELETE /secrets/{id}
* POST /services/{id}/secrets/{secretId}
* DELETE /services/{id}/secrets/{secretId}

### F-MVP-07 — Gestion des configurations [Must]

Création et édition de fichiers de configuration (application.yml, nginx.conf, logback.xml…) avec versionning, et association aux services avec chemin cible. Comme les secrets, les configs Swarm sont immuables : chaque édition crée une version.

**User stories**

* *En tant qu’opérateur, je veux éditer la configuration nginx.conf de mon service et la déployer afin d’appliquer un changement sans reconstruire l’image.*
* *En tant qu’opérateur, je veux consulter les versions précédentes d’une config afin de comprendre un changement de comportement.*

**Critères d’acceptation**

* Chaque sauvegarde crée une ConfigVersion (contenu, auteur, commentaire, horodatage) ; la version courante est pointée par la config.
* Le contenu est limité à 500 Ko (limite Swarm) ; l’encodage UTF-8 est validé.
* L’association précise le chemin cible (ex. : /etc/nginx/nginx.conf) ainsi que uid/gid/mode optionnels.
* Le déploiement d’une nouvelle version de config sur un service déclenche un rolling update.
* La suppression est refusée si la config est associée à un service.

**API**

* GET /configs
* POST /configs
* GET /configs/{id}/versions
* PUT /configs/{id} (nouvelle version)
* DELETE /configs/{id}
* POST /services/{id}/configs/{configId}
* DELETE /services/{id}/configs/{configId}

### F-MVP-08 — Moteur de déploiement Swarm [Must]

Cœur de la plateforme : traduction du modèle métier (service + ressources + env + réseaux + secrets + configs) en spécification de service Swarm, et exécution des opérations create / update / remove via l’API Docker, sans notion de stack.

**User stories**

* *En tant qu’opérateur, je veux déployer mon service en un appel afin de ne plus écrire ni appliquer de docker-stack.yml.*
* *En tant qu’opérateur, je veux suivre l’état de mon déploiement afin de savoir s’il a réussi ou échoué.*

**Critères d’acceptation**

* Le déploiement est asynchrone : l’API répond 202 avec l’identifiant du Deployment ; le statut évolue (pending → in\_progress → succeeded | failed).
* Un verrou par service interdit deux déploiements simultanés du même service (409 sinon) ; les déploiements de services différents sont parallèles.
* Un config\_snapshot complet du service est enregistré à chaque déploiement (base du diff et du rollback V1).
* Le moteur attend la convergence du service (toutes les tâches running ou échec détecté) avec un timeout configurable (défaut 5 min).
* Idempotence : redéployer un service inchangé n’interrompt pas les conteneurs (comparaison de spécification avant appel Swarm).
* Les labels Swarm portent les métadonnées plateforme (platform.service.id, platform.deployment.id) pour la réconciliation.

**API**

* POST /deployments (body : service\_id, image\_tag optionnel)
* GET /deployments/{id}
* GET /services/{id}/deployments

**Notes techniques**

* Adapter docker : utiliser ServiceCreate / ServiceUpdate / ServiceRemove du SDK officiel avec négociation de version d’API.
* L’index de version (Spec.Version.Index) doit être relu avant chaque update pour éviter les conflits de concurrence Swarm.

### F-MVP-09 — Historique des déploiements (minimal) [Must]

Conservation de chaque déploiement : date, utilisateur (ou déclencheur), version (tag d’image), résultat et durée. Exemple : wallet-api → v1.0.0, v1.0.1, v1.1.0.

**User stories**

* *En tant que tech lead, je veux consulter l’historique des versions déployées d’un service afin de corréler un incident à une mise en production.*

**Critères d’acceptation**

* Chaque entrée contient : service, tag, déclencheur (manual au MVP), utilisateur, statut, started\_at, finished\_at, message d’erreur éventuel.
* L’historique est immuable (aucune suppression ni modification par l’API).
* Liste paginée et filtrable par service, statut et période.

### F-MVP-10 — Supervision de base des services [Should]

Visualisation de l’état réel des services : Running, Pending, Failed, Updating, avec réplicas désirés vs effectifs, et détail des tâches (nœud d’exécution, état, message d’erreur).

**User stories**

* *En tant qu’opérateur, je veux voir si mon service est sain (3/3 réplicas running) afin de valider mon déploiement.*

**Critères d’acceptation**

* L’état est lu en temps réel depuis Swarm (TaskList) et agrégé : running x/y, pending, failed, updating.
* Le détail expose, par tâche : nœud, état courant, état désiré, horodatage, message d’erreur Swarm.
* Temps de réponse < 2 s pour 200 services (mise en cache courte autorisée, TTL ≤ 5 s).

**API**

* GET /services/{id}/status
* GET /services/{id}/tasks

### F-MVP-11 — API REST documentée (OpenAPI) [Must]

Contrat API complet généré (swag/OpenAPI 3), exposé via Swagger UI en environnement non-public, versionné /api/v1.

**Critères d’acceptation**

* Toutes les routes documentées : schémas de requête/réponse, codes d’erreur, exemples.
* Format d’erreur uniforme : { code, message, details } ; codes HTTP cohérents (400 validation, 401/403 auth, 404, 409 conflit, 422 règle métier).
* Le contrat OpenAPI est un artefact de build, utilisé par le frontend V1 et la CLI V3.

## 10.1 Critères de sortie du MVP

* Un service réel de l’entreprise est déployé, mis à jour et supprimé exclusivement via l’API.
* Secrets, configs et réseaux de ce service sont gérés par la plateforme.
* Couverture de tests ≥ 80 % sur domain/application ; tests d’intégration de l’adapter Docker sur un Swarm de test.
* Hors périmètre MVP (reporté V1) : interface web, GitLab, RBAC multi-rôles, stratégies de déploiement avancées, rollback.

# 11. V1 — « Industrialisation » (T0 + 6 mois)

**Objectif :** brancher la plateforme sur GitLab pour le déploiement automatique de bout en bout, livrer l’interface web Angular, le contrôle d’accès à trois rôles et les stratégies de déploiement Swarm complètes (rolling update, rollback, restart policy). À l’issue de la V1, une équipe pilote déploie sans l’aide de l’équipe DevOps.

| **ID** | **Fonctionnalité** | **Priorité** |
| --- | --- | --- |
| F-V1-01 | RBAC : rôles Admin / Operator / Viewer | Must |
| F-V1-02 | Déclaration des dépôts GitLab | Must |
| F-V1-03 | Webhooks GitLab et déploiement automatique | Must |
| F-V1-04 | Déploiement manuel d’une version (tags registry) | Must |
| F-V1-05 | Stratégie rolling update configurable | Must |
| F-V1-06 | Rollback automatique et manuel | Must |
| F-V1-07 | Restart policy | Must |
| F-V1-08 | Interface web Angular | Must |
| F-V1-09 | Historique enrichi et diff de déploiements | Should |
| F-V1-10 | Observabilité Swarm (nœuds, réplicas, santé) | Should |
| F-V1-11 | Journal d’audit | Must |

### F-V1-01 — RBAC : rôles Admin / Operator / Viewer [Must]

Gestion des utilisateurs et contrôle d’accès par rôle : Admin (gestion complète, y compris utilisateurs, secrets, réseaux), Operator (déploiement et gestion des services), Viewer (consultation seule).

**User stories**

* *En tant qu’admin, je veux créer des comptes Operator pour les développeurs afin qu’ils déploient sans pouvoir gérer les secrets du cluster.*
* *En tant que viewer, je veux consulter l’état des services afin de suivre la production sans risque de modification.*

**Critères d’acceptation**

* Matrice de droits appliquée par middleware : Viewer = GET uniquement ; Operator = Viewer + services, env, configs, déploiements, rollback ; Admin = tout (utilisateurs, secrets, réseaux, dépôts).
* Toute tentative non autorisée renvoie 403 et est journalisée dans l’audit.
* Un admin ne peut pas supprimer son propre compte ni se rétrograder s’il est le dernier admin.

**API**

* GET /users
* POST /users
* PUT /users/{id}
* DELETE /users/{id}

### F-V1-02 — Déclaration des dépôts GitLab [Must]

Association d’un service à un dépôt GitLab (ex. : wallet-api) : URL Git, branche suivie, registry d’images, token d’accès. Concrétise le principe « un dépôt = un service autonome ».

**User stories**

* *En tant qu’opérateur, je veux lier mon service à son dépôt GitLab afin que chaque build sur main soit déployable automatiquement.*

**Critères d’acceptation**

* À l’enregistrement, la plateforme valide le token (appel API GitLab) et l’accès au registry ; erreur explicite sinon.
* Le token est chiffré en base (AES-256-GCM) et jamais renvoyé par l’API.
* La plateforme génère le secret de webhook et affiche l’URL à configurer côté GitLab (ou crée le webhook automatiquement via l’API GitLab si le token le permet).
* Un dépôt n’est associé qu’à un seul service (relation 1-1, conforme au principe produit).

**API**

* GET /repositories
* POST /repositories
* PUT /repositories/{id}
* DELETE /repositories/{id}

### F-V1-03 — Webhooks GitLab et déploiement automatique [Must]

Réception des webhooks GitLab (pipeline réussi sur la branche suivie) et déclenchement automatique du rolling update avec le nouveau tag d’image. Workflow : Git Push → Pipeline GitLab → Build image → Webhook → Plateforme → Rolling update.

**User stories**

* *En tant que développeur, je veux que mon merge sur main soit déployé automatiquement afin de raccourcir le cycle de livraison.*

**Critères d’acceptation**

* Signature vérifiée (X-Gitlab-Token) ; appels invalides rejetés (401) et journalisés.
* Seuls les événements « pipeline réussi » sur la branche configurée déclenchent un déploiement ; les autres sont ignorés (réponse 200, motif tracé).
* Le tag déployé est extrait du payload selon une stratégie configurable par dépôt : tag Git, SHA court, ou variable de pipeline.
* Idempotence : un même webhook rejoué (même pipeline\_id) ne déclenche pas de second déploiement.
* Si un déploiement du service est déjà en cours, le webhook est mis en file (au plus un en attente, le dernier gagne).
* Réponse au webhook < 500 ms (202) ; traitement asynchrone.

**API**

* POST /webhooks/gitlab/{repositoryId}

### F-V1-04 — Déploiement manuel d’une version [Must]

Déploiement à la demande d’une version précise : la plateforme liste les tags disponibles dans le registry du dépôt et permet « déployer la version X », « déployer la version Y », « rollback version X ».

**User stories**

* *En tant qu’opérateur, je veux déployer la version v1.0.2 en un clic afin de livrer un hotfix sans attendre un pipeline.*

**Critères d’acceptation**

* La liste des tags est lue depuis le registry GitLab (API), triée par date, avec digest et taille.
* Le déploiement manuel exige le rôle Operator minimum et est tracé avec l’utilisateur.
* Un contrôle vérifie l’existence du tag dans le registry avant de lancer le déploiement (évite l’échec au pull).

**API**

* GET /repositories/{id}/tags
* POST /deployments (service\_id, image\_tag)

### F-V1-05 — Stratégie rolling update configurable [Must]

Paramétrage par service de la stratégie de mise à jour Swarm : parallelism, delay, failure\_action (pause | continue | rollback), monitor, max\_failure\_ratio, ordre (start-first | stop-first).

**User stories**

* *En tant qu’ingénieur DevOps, je veux configurer un rolling update 1 par 1 avec 10 s de délai afin de garantir le zéro-interruption.*

**Critères d’acceptation**

* Valeurs par défaut plateforme : parallelism=1, delay=10s, failure\_action=rollback, monitor=30s, order=start-first.
* Les paramètres sont traduits fidèlement en UpdateConfig de la spécification Swarm.
* La progression du rolling update est visible (tâches mises à jour / total) pendant le déploiement.

### F-V1-06 — Rollback automatique et manuel [Must]

Rollback automatique en cas d’échec du rolling update (selon failure\_action) et rollback manuel vers n’importe quel déploiement antérieur réussi, en s’appuyant sur le config\_snapshot.

**User stories**

* *En tant qu’opérateur, je veux revenir à la version précédente en un clic afin de réduire le temps d’incident.*
* *En tant qu’ingénieur DevOps, je veux que la plateforme annule seule un déploiement défaillant afin de protéger la production la nuit.*

**Critères d’acceptation**

* Le rollback manuel restaure le snapshot complet (image, env, ressources, secrets, configs, réseaux), pas uniquement le tag.
* Un rollback crée une nouvelle entrée Deployment (trigger=rollback) référençant le déploiement cible.
* Le rollback automatique Swarm (failure\_action=rollback) est détecté et historisé comme tel (statut rolled\_back).
* Temps de rollback manuel < 2 minutes pour un service standard (KPI).

**API**

* POST /deployments/{id}/rollback

### F-V1-07 — Restart policy [Must]

Configuration par service de la politique de redémarrage des conteneurs : Always, On-Failure, None, avec max\_attempts et window.

**Critères d’acceptation**

* Traduction fidèle vers RestartPolicy de Swarm (condition, delay, max\_attempts, window).
* Valeur par défaut : on-failure, max\_attempts=3, window=120s.
* Modification appliquée au prochain déploiement avec mention explicite dans la réponse API.

### F-V1-08 — Interface web Angular [Must]

Application web couvrant l’ensemble du périmètre : tableau de bord des services (états temps réel), fiches service (onglets : général, ressources, variables, réseaux, secrets, configs, déploiements), gestion des secrets/configs/réseaux, dépôts GitLab, utilisateurs, historique.

**User stories**

* *En tant que développeur, je veux une interface claire afin de déployer et diagnostiquer sans connaître l’API ni Swarm.*

**Critères d’acceptation**

* Le tableau de bord affiche pour chaque service : nom, version déployée, état (Running / Pending / Failed / Updating), réplicas x/y, dernier déploiement.
* Les actions affichées respectent le rôle de l’utilisateur (un Viewer ne voit aucun bouton de mutation).
* L’état des services se rafraîchit automatiquement (polling ≤ 10 s au minimum ; WebSocket en V2).
* Les formulaires valident côté client les mêmes règles que l’API (nom DNS, format des variables…).
* L’UI consomme exclusivement l’API publique v1 (aucune route privée).

### F-V1-09 — Historique enrichi et diff de déploiements [Should]

Enrichissement de l’historique : déclencheur (manuel, webhook, rollback), durée, lien pipeline GitLab, et diff de configuration entre deux déploiements (image, env, ressources, secrets, configs, réseaux).

**Critères d’acceptation**

* Le diff met en évidence champ par champ ce qui a changé entre deux snapshots (les valeurs sensibles restent masquées).
* Chaque déploiement issu d’un webhook référence l’URL du pipeline et le commit GitLab.
* L’historique global (tous services) est consultable et exportable (CSV).

**API**

* GET /deployments
* GET /deployments/{id}/diff?against={otherId}

### F-V1-10 — Observabilité Swarm (nœuds, réplicas, santé) [Should]

Vue d’infrastructure : liste des nœuds du cluster (rôle, disponibilité, ressources), répartition des tâches par nœud, santé des services.

**Critères d’acceptation**

* Liste des nœuds : hostname, rôle (manager/worker), état (ready/down), disponibilité (active/pause/drain), version Docker.
* Pour chaque service : nœuds d’exécution des tâches, état de santé agrégé (healthy/unhealthy si health check défini).
* Lecture seule au V1 (le drain/pause des nœuds reste hors périmètre).

**API**

* GET /nodes
* GET /nodes/{id}/tasks

### F-V1-11 — Journal d’audit [Must]

Trace immuable de toutes les actions de mutation : qui, quoi, quand, sur quelle ressource, depuis quelle IP.

**Critères d’acceptation**

* Couverture : auth (login/échec), utilisateurs, services, env, secrets (sans valeurs), configs, réseaux, dépôts, déploiements, rollbacks.
* Consultation filtrable (utilisateur, type de ressource, période) ; accès réservé au rôle Admin.
* Rétention 24 mois ; purge automatique au-delà.

**API**

* GET /audit-logs

## 11.1 Critères de sortie de la V1

* Chaîne complète démontrée : push Git → pipeline → webhook → rolling update → historique, sans intervention manuelle.
* Une équipe pilote (hors DevOps) utilise l’UI pour déployer en autonomie pendant 2 semaines.
* Rollback manuel testé en production réelle avec un temps < 2 minutes.

# 12. V2 — « Autonomie & observabilité » (T0 + 9 mois)

**Objectif :** donner aux équipes tout ce qu’il faut pour diagnostiquer et opérer leurs services sans accès SSH ni docker CLI : logs temps réel, métriques, événements, notifications. La V2 inclut aussi l’outil de migration des stacks existantes, condition de l’objectif « ≥ 90 % de services migrés ».

| **ID** | **Fonctionnalité** | **Priorité** |
| --- | --- | --- |
| F-V2-01 | Logs des services en temps réel | Must |
| F-V2-02 | Métriques conteneurs (CPU, mémoire, réseau) | Must |
| F-V2-03 | Timeline des événements | Should |
| F-V2-04 | Notifications (Slack, email, webhooks sortants) | Must |
| F-V2-05 | Health checks configurables | Must |
| F-V2-06 | Gestion des volumes et montages | Must |
| F-V2-07 | Templates de services | Should |
| F-V2-08 | Versionning avancé des configurations (diff, restauration) | Should |
| F-V2-09 | Contraintes de placement et labels | Should |
| F-V2-10 | Import des stacks existantes (migration assistée) | Must |
| F-V2-11 | Scaling manuel rapide | Could |

### F-V2-01 — Logs des services en temps réel [Must]

Streaming des logs des conteneurs d’un service dans l’UI (WebSocket/SSE), avec filtre par tâche, recherche plein texte côté client, sélection de période et téléchargement.

**User stories**

* *En tant que développeur, je veux suivre les logs de mon service pendant un déploiement afin de diagnostiquer un démarrage défaillant sans accès SSH.*

**Critères d’acceptation**

* Latence d’affichage < 2 s ; multiplexage des réplicas avec préfixe de tâche.
* Accès aux logs soumis au RBAC (Viewer minimum) et tracé dans l’audit.
* Téléchargement des N dernières lignes (configurable, défaut 10 000) au format texte.

**API**

* GET /services/{id}/logs?follow=true&since=…&tail=…

### F-V2-02 — Métriques conteneurs [Must]

CPU, mémoire et réseau par service et par tâche (API stats Docker), avec historique court (24 h) et affichage des limites configurées pour repérer les services sous-dimensionnés.

**Critères d’acceptation**

* Collecte ≤ 30 s d’intervalle ; agrégation par service ; rétention 24 h en base (au-delà : V4 avec Prometheus).
* Graphiques UI : CPU vs limite, mémoire vs limite ; alerte visuelle au-delà de 80 % de la limite.

**API**

* GET /services/{id}/metrics?from=…&to=…

### F-V2-03 — Timeline des événements [Should]

Chronologie unifiée par service : événements Swarm (création/échec/redémarrage de tâches) et événements plateforme (déploiements, rotations de secrets, changements de config).

**Critères d’acceptation**

* Consommation du flux d’événements Docker (events) avec reconnexion automatique.
* Chaque événement est typé, horodaté, et corrélé au déploiement en cours le cas échéant.

**API**

* GET /services/{id}/events

### F-V2-04 — Notifications [Must]

Notifications sortantes sur les événements clés : déploiement réussi/échoué, rollback, service unhealthy. Canaux : Slack (et compatibles), email (SMTP), webhooks sortants génériques.

**User stories**

* *En tant qu’équipe, nous voulons être notifiés dans notre canal Slack quand un déploiement échoue afin de réagir immédiatement.*

**Critères d’acceptation**

* Règles de notification par service ou globales : événement → canal(aux).
* Les messages contiennent : service, version, déclencheur, résultat, lien direct vers la plateforme.
* Échecs d’envoi journalisés avec retry exponentiel (3 tentatives).

**API**

* GET /notification-channels
* POST /notification-channels
* POST /services/{id}/notification-rules

### F-V2-05 — Health checks configurables [Must]

Définition par service du health check (commande, intervalle, timeout, retries, start\_period), traduit dans la spécification Swarm et exploité par les rolling updates et l’affichage de santé.

**Critères d’acceptation**

* Le health check est pris en compte par le rolling update (une tâche n’est saine que healthy, ce qui fiabilise failure\_action=rollback).
* L’état healthy/unhealthy apparaît au niveau tâche et agrégé au niveau service.

### F-V2-06 — Gestion des volumes et montages [Must]

Déclaration des montages d’un service : volumes nommés, bind mounts (restreints aux admins), tmpfs ; visualisation des volumes du cluster.

**Critères d’acceptation**

* Création/suppression de volumes nommés ; suppression refusée si le volume est monté par un service.
* Les bind mounts sont réservés au rôle Admin (risque sécurité) et journalisés.
* Avertissement explicite sur les volumes locaux avec services multi-réplicas (non partagés entre nœuds).

**API**

* GET /volumes
* POST /volumes
* DELETE /volumes/{id}
* PUT /services/{id}/mounts

### F-V2-07 — Templates de services [Should]

Création de services à partir de modèles internes (ex. : « API Java », « worker Python », « frontend nginx ») pré-remplissant ressources, health check, réseaux, stratégie de déploiement.

**Critères d’acceptation**

* Un template définit des valeurs par défaut et des champs verrouillables (ex. : limites mémoire imposées).
* Les templates sont versionnés et gérés par les admins ; tout Operator peut instancier.

**API**

* GET /templates
* POST /templates
* POST /services/from-template/{templateId}

### F-V2-08 — Versionning avancé des configurations [Should]

Diff visuel entre deux versions d’une config, restauration d’une version antérieure (création d’une nouvelle version au contenu identique), commentaire obligatoire par version.

**Critères d’acceptation**

* Diff ligne à ligne dans l’UI ; restauration en un clic suivie d’une proposition de redéploiement des services associés.
* La liste des services impactés par une version de config est visible avant application.

### F-V2-09 — Contraintes de placement et labels [Should]

Gestion des labels de nœuds et des contraintes de placement par service (ex. : node.labels.disk==ssd), plus préférences de répartition (spread).

**Critères d’acceptation**

* Édition des labels de nœuds réservée aux admins ; les contraintes invalides sont détectées (aucun nœud éligible → avertissement avant déploiement).

**API**

* PUT /nodes/{id}/labels
* PUT /services/{id}/placement

### F-V2-10 — Import des stacks existantes (migration assistée) [Must]

Outil d’import qui analyse un docker-stack.yml (ou les services Swarm existants) et propose la création des entités plateforme correspondantes : services, réseaux, secrets, configs, montages.

**User stories**

* *En tant qu’ingénieur DevOps, je veux importer mes stacks existantes afin de migrer vers la plateforme sans tout ressaisir.*

**Critères d’acceptation**

* L’import produit un rapport de couverture : éléments repris automatiquement, éléments à compléter (valeurs de secrets, tokens), éléments non supportés.
* Mode « adoption » : la plateforme peut adopter un service Swarm déjà déployé (pose de labels) sans le redéployer.
* Aucune perte de service pendant l’adoption ; rollback de l’adoption possible.

**API**

* POST /imports/stack-file
* POST /imports/adopt-service

### F-V2-11 — Scaling manuel rapide [Could]

Modification du nombre de réplicas en une action (UI et API) sans passer par un déploiement complet.

**Critères d’acceptation**

* Le scaling est historisé comme un déploiement léger (trigger=scale) ; RBAC Operator minimum.

**API**

* POST /services/{id}/scale (body : replicas)

## 12.1 Critères de sortie de la V2

* ≥ 90 % des services historiques migrés ou adoptés ; plus aucun déploiement via docker stack deploy.
* Les développeurs diagnostiquent leurs incidents via logs/métriques/événements sans accès SSH.
* Les échecs de déploiement notifient automatiquement les équipes concernées.

# 13. V3 — « Échelle » (T0 + 14 mois)

**Objectif :** faire passer la plateforme du statut d’outil mono-cluster à celui d’infrastructure critique : plusieurs clusters, agents sécurisés sur les nœuds (suppression de l’accès direct au socket Docker), CLI scriptable, exposition HTTP native via Traefik avec TLS automatique, SSO d’entreprise et haute disponibilité de la plateforme elle-même.

| **ID** | **Fonctionnalité** | **Priorité** |
| --- | --- | --- |
| F-V3-01 | Multi-cluster Swarm | Must |
| F-V3-02 | Agent d’exécution sur les clusters (mTLS) | Must |
| F-V3-03 | CLI dédiée | Must |
| F-V3-04 | Gestion native de Traefik (routes, domaines) | Must |
| F-V3-05 | Certificats TLS (Let’s Encrypt, import) | Must |
| F-V3-06 | SSO (OIDC/LDAP), 2FA, tokens d’API | Must |
| F-V3-07 | Environnements et promotion (staging → prod) | Should |
| F-V3-08 | Haute disponibilité de la plateforme | Must |
| F-V3-09 | Quotas et limites par équipe | Could |

### F-V3-01 — Multi-cluster Swarm [Must]

Enregistrement de plusieurs clusters Swarm (production, staging, edge…) ; chaque service est rattaché à un cluster cible ; réseaux, secrets et configs deviennent des ressources par cluster.

**Critères d’acceptation**

* Vue consolidée multi-clusters (états, versions) et vues par cluster.
* Les identifiants/ressources Swarm sont qualifiés par cluster ; aucune fuite d’un cluster vers l’autre.
* La perte de connexion à un cluster est signalée sans affecter le pilotage des autres.

**API**

* GET /clusters
* POST /clusters
* GET /clusters/{id}/health

### F-V3-02 — Agent d’exécution sur les clusters [Must]

Agent léger (Go) déployé sur les managers de chaque cluster, établissant une connexion sortante persistante (mTLS) vers la plateforme. Supprime l’exposition du socket Docker et permet de piloter des clusters derrière NAT/firewall.

**Critères d’acceptation**

* Enrôlement par token à usage unique ; rotation automatique des certificats mTLS.
* L’agent n’exécute que des opérations signées provenant de la plateforme ; liste blanche d’opérations.
* Mode dégradé : si la plateforme est injoignable, les services en place continuent de tourner ; l’agent resynchronise au retour.
* Compatibilité ascendante : le mode « socket direct » du MVP reste supporté pour les petits déploiements.

### F-V3-03 — CLI dédiée [Must]

CLI (binaire Go unique) couvrant les opérations courantes : login, liste des services, deploy, rollback, logs, scale, gestion des configs. Utilisable en CI comme alternative aux webhooks.

**User stories**

* *En tant que développeur, je veux taper « platform deploy wallet-api --tag v1.2.0 » afin de déployer depuis mon terminal ou ma CI.*

**Critères d’acceptation**

* Authentification par token d’API (F-V3-06) ; sortie table et JSON (--output json) pour le scripting.
* Code retour non nul si le déploiement échoue ; option --wait pour attendre la convergence.
* La CLI est générée/alignée sur le contrat OpenAPI ; compatibilité descendante garantie sur une version majeure.

### F-V3-04 — Gestion native de Traefik [Must]

Exposition HTTP(S) des services sans écrire de labels Traefik : l’utilisateur déclare domaine, chemin, port interne, middlewares (redirection, auth basique, rate limit) ; la plateforme génère les labels.

**Critères d’acceptation**

* Le routing est validé avant déploiement (collision de domaines/chemins détectée entre services).
* Les middlewares proposés sont un catalogue maîtrisé par les admins.
* Vue d’ensemble des routes publiées par cluster.

**API**

* GET /routes
* PUT /services/{id}/routes

### F-V3-05 — Certificats TLS [Must]

Certificats automatiques Let’s Encrypt (via Traefik) pour les routes publiées, import de certificats d’entreprise, tableau de bord des expirations.

**Critères d’acceptation**

* Renouvellement automatique ; alerte (notification V2) 30 jours avant expiration d’un certificat importé.
* Les clés privées importées sont chiffrées at-rest et non relisibles.

### F-V3-06 — SSO, 2FA et tokens d’API [Must]

Authentification d’entreprise : OIDC (et/ou LDAP), provisioning des rôles par groupes, 2FA TOTP pour les comptes locaux, tokens d’API nominatifs et de service (scopés, expirables, révocables) pour la CLI et la CI.

**Critères d’acceptation**

* Le mapping groupe SSO → rôle plateforme est configurable ; révocation effective ≤ 5 min après désactivation côté IdP.
* Chaque token d’API a un scope (lecture, déploiement, admin), une expiration et apparaît dans l’audit avec son identité propre.

**API**

* POST /api-tokens
* DELETE /api-tokens/{id}

### F-V3-07 — Environnements et promotion [Should]

Notion d’environnement (dev, staging, prod) reliant un même service logique à plusieurs déploiements (souvent sur des clusters différents), avec promotion d’une version validée d’un environnement vers le suivant.

**Critères d’acceptation**

* La promotion reprend la version exacte (digest d’image) validée en staging, pas un rebuild.
* Les variables/configs/secrets sont définis par environnement avec héritage des valeurs communes.
* La promotion vers la production peut exiger une approbation (préfiguration des workflows V4).

**API**

* POST /services/{id}/promote (from, to)

### F-V3-08 — Haute disponibilité de la plateforme [Must]

La plateforme elle-même devient hautement disponible : backend stateless multi-réplicas derrière un load balancer, PostgreSQL répliqué, migrations sans interruption, verrous distribués pour le moteur de déploiement.

**Critères d’acceptation**

* Objectif 99,9 % ; perte d’une instance sans interruption de service ni déploiement corrompu.
* Les verrous de déploiement par service fonctionnent en multi-instance (verrou en base ou advisory lock PostgreSQL).
* Procédure de restauration documentée et testée (RPO ≤ 24 h, RTO ≤ 1 h).

### F-V3-09 — Quotas et limites par équipe [Could]

Regroupement des services par équipe/projet avec quotas : nombre de services, CPU/mémoire cumulés, nombre de déploiements par jour.

**Critères d’acceptation**

* Le dépassement de quota bloque la création/le scaling avec un message explicite ; les admins peuvent outrepasser.

## 13.1 Critères de sortie de la V3

* Deux clusters pilotés en production depuis une instance HA, via agents mTLS (plus aucun socket Docker exposé).
* La CLI est utilisée dans au moins un pipeline CI en remplacement d’un webhook.
* 100 % des routes HTTP publiques gérées par la plateforme (Traefik + TLS automatique).

# 14. V4 — « Déploiements avancés & GitOps » (T0 + 20 mois)

**Objectif :** élever le niveau de maturité des mises en production : état désiré décrit dans Git et réconcilié en continu (GitOps), stratégies blue/green et canary appuyées sur Traefik, workflows d’approbation, et monitoring Prometheus/Grafana intégré qui alimente l’analyse automatique des canaries.

| **ID** | **Fonctionnalité** | **Priorité** |
| --- | --- | --- |
| F-V4-01 | GitOps : réconciliation déclarative | Must |
| F-V4-02 | Blue/Green deployment | Must |
| F-V4-03 | Canary deployment avec analyse automatique | Must |
| F-V4-04 | Workflows d’approbation et fenêtres de déploiement | Must |
| F-V4-05 | Monitoring intégré Prometheus / Grafana | Must |
| F-V4-06 | Alerting sur métriques | Should |
| F-V4-07 | Marketplace de templates | Should |
| F-V4-08 | Tests post-déploiement (smoke tests) | Should |

### F-V4-01 — GitOps : réconciliation déclarative [Must]

L’état désiré des services (manifeste YAML au format plateforme, pas docker-stack.yml) est versionné dans un dépôt Git. La plateforme surveille le dépôt, applique les changements et détecte les dérives (drift) entre l’état déclaré, son modèle et l’état réel du cluster.

**User stories**

* *En tant qu’ingénieur DevOps, je veux que la production reflète exactement le contenu du dépôt Git afin que chaque changement passe par une merge request auditée.*

**Critères d’acceptation**

* Boucle de réconciliation : détection d’un commit → plan (diff) → application → rapport ; intervalle ≤ 1 min.
* Le drift (modification manuelle hors Git) est détecté, signalé, et corrigé automatiquement ou sur validation, selon la politique configurée.
* Les services peuvent être gérés en mode UI ou en mode GitOps ; le mode GitOps verrouille les mutations UI (lecture seule + lien vers le dépôt).
* Le format de manifeste est documenté, validé par schéma (JSON Schema), et exportable depuis un service existant (bascule UI → GitOps en un clic).

### F-V4-02 — Blue/Green deployment [Must]

Déploiement de la nouvelle version (green) en parallèle de l’actuelle (blue), tests sur une URL de prévisualisation, puis bascule instantanée du trafic via Traefik et retrait de l’ancienne version.

**Critères d’acceptation**

* La bascule de trafic est atomique et réversible en < 10 s (re-bascule vers blue).
* Une URL de prévisualisation privée permet de tester green avant bascule.
* Les ressources de l’ancienne couleur sont conservées un délai configurable avant nettoyage automatique.
* Limitation documentée : nécessite des services sans état ou à état externe ; contrôle préalable sur la présence de volumes.

**API**

* POST /services/{id}/deployments/blue-green
* POST /deployments/{id}/switch
* POST /deployments/{id}/abort

### F-V4-03 — Canary deployment avec analyse automatique [Must]

Montée en charge progressive de la nouvelle version (ex. : 5 % → 25 % → 50 % → 100 % du trafic, pondération Traefik), avec analyse automatique des métriques (taux d’erreur, latence) entre chaque palier et rollback automatique si les seuils sont dépassés.

**Critères d’acceptation**

* Les paliers, durées d’observation et seuils (ex. : erreurs 5xx < 1 %, p95 < seuil) sont configurables par service.
* L’analyse s’appuie sur les métriques Prometheus (F-V4-05) ; sans métriques disponibles, le canary passe en validation manuelle par palier.
* Tout dépassement de seuil déclenche le rollback automatique et une notification ; chaque palier est historisé.

### F-V4-04 — Workflows d’approbation et fenêtres de déploiement [Must]

Validation à quatre yeux pour les environnements protégés (la production exige l’approbation d’un autre utilisateur habilité), fenêtres de déploiement autorisées et périodes de gel (freeze).

**Critères d’acceptation**

* Un déploiement en attente d’approbation est visible, approuvable/rejetable avec commentaire ; l’auteur ne peut pas s’auto-approuver.
* Les webhooks reçus pendant un gel sont mis en attente et listés ; un admin peut forcer avec justification (tracée).
* Toutes les décisions d’approbation sont dans le journal d’audit.

**API**

* GET /approvals
* POST /approvals/{id}/approve
* POST /approvals/{id}/reject

### F-V4-05 — Monitoring intégré Prometheus / Grafana [Must]

Provisioning automatique de la pile de monitoring : exporters sur les nœuds, scrape des services qui exposent /metrics, dashboards Grafana générés par service, rétention longue des métriques (remplace la rétention 24 h de la V2).

**Critères d’acceptation**

* Un service marqué « monitoré » est scrappé sans configuration manuelle de Prometheus (génération de la configuration de scrape).
* Dashboards générés : ressources, trafic Traefik (taux de requêtes, erreurs, latence), santé des réplicas.
* Les graphiques clés sont intégrés dans l’UI de la plateforme (iframe/API Grafana), avec lien vers Grafana pour l’analyse approfondie.

### F-V4-06 — Alerting sur métriques [Should]

Règles d’alerte par service (CPU, mémoire, taux d’erreur, latence, réplicas indisponibles) routées vers les canaux de notification de la V2.

**Critères d’acceptation**

* Bibliothèque de règles prêtes à l’emploi ; seuils personnalisables ; déduplication et regroupement des alertes.
* Chaque alerte référence le service et le déploiement potentiellement responsable (corrélation temporelle).

### F-V4-07 — Marketplace de templates [Should]

Catalogue organisé de templates (V2) enrichi : publication par les équipes, versionning sémantique, paramètres typés à l’instanciation, et composition multi-services (ex. : « app + worker + redis » instancié en trois services liés).

**Critères d’acceptation**

* Un template composite crée plusieurs services, leurs réseaux et associations en une transaction (tout ou rien).
* Processus de publication avec revue par un admin ; les mises à jour de template n’affectent pas les services déjà instanciés.

### F-V4-08 — Tests post-déploiement (smoke tests) [Should]

Exécution automatique de vérifications après chaque déploiement : appels HTTP attendus (code, contenu), commande dans un conteneur éphémère ; le résultat conditionne le statut final et peut déclencher le rollback.

**Critères d’acceptation**

* Les smoke tests sont définis par service (suite ordonnée, timeout global) ; résultat visible dans l’historique du déploiement.
* En cas d’échec, comportement configurable : marquer failed, notifier, ou rollback automatique.

## 14.1 Critères de sortie de la V4

* Au moins un service critique déployé en canary automatisé avec analyse de métriques en production.
* Les services de production gérés en GitOps avec drift detection active.
* Tout déploiement en production passe par approbation ou pipeline GitOps audité.

# 15. V5 — « Au-delà de Swarm » (T0 + 26 mois)

**Objectif :** capitaliser sur l’architecture hexagonale pour ouvrir la plateforme : le même modèle métier (service, réseau, secret, config, déploiement) se déploie sur Kubernetes via un nouvel adapter, l’autoscaling devient natif, et la plateforme s’expose à l’écosystème (API publique, SDK, Terraform, plugins).

| **ID** | **Fonctionnalité** | **Priorité** |
| --- | --- | --- |
| F-V5-01 | Abstraction multi-orchestrateur | Must |
| F-V5-02 | Adapter Kubernetes | Must |
| F-V5-03 | Migration assistée Swarm → Kubernetes | Should |
| F-V5-04 | Autoscaling horizontal | Must |
| F-V5-05 | FinOps : consommation et coûts par service/équipe | Should |
| F-V5-06 | API publique versionnée, SDK et provider Terraform | Should |
| F-V5-07 | Système de plugins / extensibilité | Could |
| F-V5-08 | Assistant de diagnostic des déploiements | Could |

### F-V5-01 — Abstraction multi-orchestrateur [Must]

Consolidation du port Orchestrator : le domaine et les cas d’usage deviennent strictement agnostiques du runtime ; chaque cluster déclare son type (swarm | kubernetes) et ses capacités (capabilities) ; les fonctionnalités non supportées par un runtime sont explicitement dégradées.

**Critères d’acceptation**

* Matrice de capacités par type de cluster, exposée par l’API et reflétée dans l’UI (une option non supportée est masquée/désactivée avec explication).
* Aucune référence à Swarm ou Kubernetes dans les couches domain et application (vérifié par lint d’architecture en CI).

### F-V5-02 — Adapter Kubernetes [Must]

Implémentation du port Orchestrator pour Kubernetes : Service plateforme → Deployment + Service K8s ; routes → Ingress ; secrets → Secrets ; configs → ConfigMaps ; stratégies → RollingUpdate ; un namespace par équipe/projet.

**Critères d’acceptation**

* Parité fonctionnelle sur le périmètre cœur : déploiement, rollback, env, secrets, configs, réseaux (Network Policies), logs, métriques, scaling.
* Le déploiement automatique GitLab fonctionne à l’identique quel que soit le runtime du cluster cible.
* Les écarts de parité sont documentés dans la matrice de capacités (F-V5-01).

### F-V5-03 — Migration assistée Swarm → Kubernetes [Should]

Assistant de migration d’un service d’un cluster Swarm vers un cluster Kubernetes : conversion du modèle, recréation des secrets/configs, déploiement parallèle, bascule DNS/route, puis retrait de l’ancien déploiement.

**Critères d’acceptation**

* Rapport de compatibilité avant migration (volumes, contraintes de placement, fonctionnalités non transposables).
* Migration réversible jusqu’à la bascule de trafic ; les deux déploiements coexistent pendant la validation.

### F-V5-04 — Autoscaling horizontal [Must]

Ajustement automatique du nombre de réplicas en fonction des métriques (CPU, mémoire, requêtes/s Traefik) : natif (HPA) sur Kubernetes, implémenté par la boucle de contrôle de la plateforme sur Swarm.

**Critères d’acceptation**

* Bornes min/max obligatoires ; stabilisation anti-oscillation (cooldown configurable).
* Chaque décision de scaling est historisée avec la métrique déclencheuse.
* Compatible avec les quotas d’équipe (V3) : l’autoscaling ne dépasse jamais le quota.

**API**

* PUT /services/{id}/autoscaling

### F-V5-05 — FinOps : consommation et coûts [Should]

Tableaux de bord de consommation des ressources (CPU·h, Go·h, trafic) par service, équipe et environnement, avec coûts estimés à partir d’une grille tarifaire configurable et rapports mensuels exportables.

**Critères d’acceptation**

* Les coûts estimés sont rapprochables des métriques Prometheus (cohérence ± 5 %).
* Rapport mensuel automatique par équipe (notification + export CSV/PDF).

### F-V5-06 — API publique, SDK et provider Terraform [Should]

Stabilisation du contrat API en version publique documentée avec politique de dépréciation, SDK Go et TypeScript générés depuis OpenAPI, et provider Terraform couvrant services, secrets, configs, réseaux et routes.

**Critères d’acceptation**

* Politique de versionnage publiée : pas de breaking change sans nouvelle version majeure et préavis de 6 mois.
* Le provider Terraform permet de recréer un environnement complet (services + dépendances) depuis zéro.

### F-V5-07 — Système de plugins [Could]

Points d’extension (hooks) sur le cycle de vie des déploiements (pré/post), sources SCM additionnelles (GitHub, Bitbucket via le port SCMProvider), et canaux de notification personnalisés.

**Critères d’acceptation**

* Les plugins s’exécutent de façon isolée (processus séparé ou webhook sortant) : un plugin défaillant ne bloque jamais un déploiement au-delà de son timeout.

### F-V5-08 — Assistant de diagnostic des déploiements [Could]

Analyse automatique des échecs de déploiement (logs, événements, métriques, historique) produisant un diagnostic lisible (ex. : « OOMKilled : la limite mémoire de 256M est insuffisante, pic observé à 410M ») et des recommandations actionnables.

**Critères d’acceptation**

* Le diagnostic est joint à l’entrée d’historique du déploiement échoué et inclus dans la notification.
* Les recommandations proposent une action en un clic quand c’est possible (ex. : augmenter la limite mémoire et redéployer).

## 15.1 Critères de sortie de la V5

* Un service de production déployé sur Kubernetes via le même modèle et le même pipeline que sur Swarm.
* Autoscaling actif sur au moins un service à trafic variable.
* Un environnement complet reconstructible via Terraform.

# 16. Risques et mitigations

| **Risque** | **Impact** | **Prob.** | **Mitigation** |
| --- | --- | --- | --- |
| Exposition du socket Docker du manager (MVP→V2) | Élevé | Moyenne | Accès réseau restreint à la plateforme ; TLS ; remplacement par les agents mTLS en V3. |
| Secrets/configs immuables dans Swarm | Moyen | Haute | Stratégie de versionnage (suffixe \_vN) et bascule orchestrée intégrée dès le MVP (F-MVP-06/07). |
| Dérive entre le modèle plateforme et l’état réel du cluster | Élevé | Moyenne | Labels de réconciliation dès le MVP ; détection de drift en V4 (GitOps) ; la base fait foi pour le modèle. |
| Couplage fort à GitLab | Moyen | Faible | Port SCMProvider abstrait dès le MVP ; autres SCM possibles en V5 (plugins). |
| Adoption insuffisante par les équipes | Élevé | Moyenne | Approche API-first puis UI soignée (V1), import/adoption des stacks existantes (V2), équipe pilote et formation. |
| Plateforme = point de défaillance du processus de livraison | Élevé | Faible | Les services déployés ne dépendent pas de la plateforme à l’exécution ; HA en V3 ; déploiement d’urgence documenté via docker CLI. |
| Montée en charge du flux d’événements/logs | Moyen | Moyenne | Backpressure, échantillonnage, rétention courte en V2 puis délégation à Prometheus/loki en V4. |
| Dette sur l’abstraction Orchestrator (fuites Swarm dans le domaine) | Élevé | Moyenne | Lint d’architecture en CI, revues dédiées, matrice de capacités formalisée en V5. |
| Sécurité des webhooks (forge de requêtes) | Élevé | Faible | Secret par dépôt, vérification stricte, idempotence par pipeline\_id, rate limiting, audit. |

# 17. Annexe A — Récapitulatif des endpoints API

Contrat cible (préfixe /api/v1). La colonne « Version » indique la version d’introduction.

| **Méthode** | **Route** | **Description** | **Rôle min.** | **Version** |
| --- | --- | --- | --- | --- |
| POST | /auth/login | Authentification | — | MVP |
| POST | /auth/refresh | Renouvellement du token | — | MVP |
| GET | /services | Liste des services | Viewer | MVP |
| POST | /services | Création d’un service | Operator | MVP |
| PUT | /services/{id} | Modification d’un service | Operator | MVP |
| DELETE | /services/{id} | Suppression d’un service | Operator | MVP |
| PUT | /services/{id}/env | Variables d’environnement | Operator | MVP |
| GET | /services/{id}/status | État temps réel | Viewer | MVP |
| GET | /services/{id}/logs | Logs (streaming) | Viewer | V2 |
| GET | /services/{id}/metrics | Métriques | Viewer | V2 |
| POST | /services/{id}/scale | Scaling manuel | Operator | V2 |
| GET | /networks | Liste des réseaux | Viewer | MVP |
| POST | /networks | Création d’un réseau | Admin | MVP |
| DELETE | /networks/{id} | Suppression d’un réseau | Admin | MVP |
| GET | /secrets | Liste des secrets (métadonnées) | Viewer | MVP |
| POST | /secrets | Création d’un secret | Admin | MVP |
| PUT | /secrets/{id} | Rotation d’un secret | Admin | MVP |
| DELETE | /secrets/{id} | Suppression d’un secret | Admin | MVP |
| GET | /configs | Liste des configurations | Viewer | MVP |
| POST | /configs | Création d’une configuration | Operator | MVP |
| PUT | /configs/{id} | Nouvelle version de configuration | Operator | MVP |
| GET | /configs/{id}/versions | Historique des versions | Viewer | MVP |
| POST | /deployments | Déclenchement d’un déploiement | Operator | MVP |
| GET | /deployments/{id} | Détail d’un déploiement | Viewer | MVP |
| POST | /deployments/{id}/rollback | Rollback | Operator | V1 |
| GET | /deployments/{id}/diff | Diff entre déploiements | Viewer | V1 |
| GET | /repositories | Dépôts GitLab | Viewer | V1 |
| POST | /repositories | Déclaration d’un dépôt | Operator | V1 |
| GET | /repositories/{id}/tags | Tags du registry | Viewer | V1 |
| POST | /webhooks/gitlab/{repoId} | Webhook entrant GitLab | — (signé) | V1 |
| GET | /nodes | Nœuds du cluster | Viewer | V1 |
| GET | /audit-logs | Journal d’audit | Admin | V1 |
| GET | /volumes | Volumes | Viewer | V2 |
| GET | /clusters | Clusters | Viewer | V3 |
| PUT | /services/{id}/routes | Routes Traefik | Operator | V3 |
| POST | /services/{id}/promote | Promotion d’environnement | Operator | V3 |
| POST | /approvals/{id}/approve | Approbation d’un déploiement | Operator | V4 |
| PUT | /services/{id}/autoscaling | Règles d’autoscaling | Operator | V5 |

# 18. Annexe B — Glossaire

| **Terme** | **Définition** |
| --- | --- |
| Stack | Groupe de services Swarm défini par un fichier docker-stack.yml — notion que la plateforme fait disparaître pour l’utilisateur. |
| Service (Swarm) | Unité de déploiement Swarm : image + réplicas + configuration, exécutée sous forme de tâches. |
| Tâche (task) | Instance d’un service : un conteneur planifié sur un nœud. |
| Réseau overlay | Réseau virtuel multi-nœuds permettant la communication entre services du cluster. |
| Secret / Config Swarm | Objets immuables distribués de façon chiffrée (secrets) aux conteneurs ; toute modification nécessite une nouvelle version. |
| Rolling update | Mise à jour progressive des tâches d’un service, par lots, avec délai et surveillance. |
| Rollback | Retour à un état de déploiement antérieur (snapshot complet dans la plateforme). |
| Blue/Green | Deux environnements parallèles ; bascule de trafic instantanée de l’ancien (blue) vers le nouveau (green). |
| Canary | Exposition progressive d’une nouvelle version à une fraction croissante du trafic, sous surveillance de métriques. |
| GitOps | Pratique où l’état désiré de l’infrastructure est décrit dans Git et appliqué/réconcilié automatiquement. |
| Drift | Écart entre l’état désiré (Git/modèle) et l’état réel du cluster. |
| mTLS | TLS mutuel : client et serveur s’authentifient par certificat (plateforme ↔ agents). |
| RBAC | Contrôle d’accès basé sur les rôles (Admin, Operator, Viewer). |