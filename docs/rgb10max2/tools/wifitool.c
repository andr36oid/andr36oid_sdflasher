/* wifitool - minimal nl80211 control (no libnl, static)
 * usage: wifitool ps-off | ps-on | ps-get | reg MA
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <net/if.h>
#include <sys/socket.h>
#include <linux/netlink.h>
#include <linux/genetlink.h>
#include <linux/nl80211.h>

#ifndef NLA_OK
#define NLA_OK(nla, len) ((len) >= (int)sizeof(struct nlattr) && \
    (nla)->nla_len >= sizeof(struct nlattr) && (nla)->nla_len <= (len))
#endif
#ifndef NLA_NEXT
#define NLA_NEXT(nla, attrlen) ((attrlen) -= NLA_ALIGN((nla)->nla_len), \
    (struct nlattr *)(((char *)(nla)) + NLA_ALIGN((nla)->nla_len)))
#endif

static int nl_open(void)
{
    int fd = socket(AF_NETLINK, SOCK_RAW, NETLINK_GENERIC);
    if (fd < 0) { perror("socket"); return -1; }
    struct sockaddr_nl sa = { .nl_family = AF_NETLINK };
    if (bind(fd, (struct sockaddr *)&sa, sizeof(sa)) < 0) { perror("bind"); close(fd); return -1; }
    return fd;
}

/* send one message and wait for the ACK; returns 0 on success */
static int nl_talk(int fd, struct nlmsghdr *nlh)
{
    struct sockaddr_nl sa = { .nl_family = AF_NETLINK };
    nlh->nlmsg_seq = 1;
    if (sendto(fd, nlh, nlh->nlmsg_len, 0, (struct sockaddr *)&sa, sizeof(sa)) < 0) {
        perror("send"); return -1;
    }
    char buf[8192];
    int r = recv(fd, buf, sizeof(buf), 0);
    if (r < 0) { perror("recv"); return -1; }
    for (struct nlmsghdr *h = (struct nlmsghdr *)buf; NLMSG_OK(h, r); h = NLMSG_NEXT(h, r)) {
        if (h->nlmsg_type == NLMSG_ERROR) {
            struct nlmsgerr *e = (struct nlmsgerr *)NLMSG_DATA(h);
            if (e->error) { errno = -e->error; perror("netlink"); return -1; }
            return 0;
        }
        if (h->nlmsg_type == NLMSG_DONE) return 0;
    }
    return 0;
}

#define NLA_PUT(nlh, type, len, data) do { \
    int _l = NLA_HDRLEN + (len); \
    struct nlattr *_a = (struct nlattr *)((char *)(nlh) + NLMSG_ALIGN((nlh)->nlmsg_len)); \
    _a->nla_type = (type); _a->nla_len = _l; \
    memcpy((char *)_a + NLA_HDRLEN, (data), (len)); \
    (nlh)->nlmsg_len = NLMSG_ALIGN((nlh)->nlmsg_len) + NLA_ALIGN(_l); \
} while (0)

static int resolve_nl80211(int fd)
{
    char msg[512] = {0};
    struct nlmsghdr *nlh = (struct nlmsghdr *)msg;
    nlh->nlmsg_len = NLMSG_LENGTH(GENL_HDRLEN);
    nlh->nlmsg_type = GENL_ID_CTRL;
    nlh->nlmsg_flags = NLM_F_REQUEST;
    struct genlmsghdr *g = (struct genlmsghdr *)NLMSG_DATA(nlh);
    g->cmd = CTRL_CMD_GETFAMILY;
    g->version = 1;
    const char name[] = "nl80211";
    NLA_PUT(nlh, CTRL_ATTR_FAMILY_NAME, sizeof(name), name);

    struct sockaddr_nl sa = { .nl_family = AF_NETLINK };
    nlh->nlmsg_seq = 1;
    sendto(fd, nlh, nlh->nlmsg_len, 0, (struct sockaddr *)&sa, sizeof(sa));
    char buf[8192];
    int r = recv(fd, buf, sizeof(buf), 0);
    if (r < 0) { perror("recv"); return -1; }
    for (struct nlmsghdr *h = (struct nlmsghdr *)buf; NLMSG_OK(h, r); h = NLMSG_NEXT(h, r)) {
        if (h->nlmsg_type == GENL_ID_CTRL) {
            struct genlmsghdr *gg = (struct genlmsghdr *)NLMSG_DATA(h);
            int len = h->nlmsg_len - NLMSG_LENGTH(GENL_HDRLEN);
            for (struct nlattr *a = (struct nlattr *)((char *)gg + GENL_HDRLEN);
                 NLA_OK(a, len); a = NLA_NEXT(a, len)) {
                if (a->nla_type == CTRL_ATTR_FAMILY_ID) {
                    return *(unsigned short *)((char *)a + NLA_HDRLEN);
                }
            }
        }
    }
    return -1;
}

static int ps_set(int fd, int family, const char *ifname, unsigned int state)
{
    char msg[512] = {0};
    struct nlmsghdr *nlh = (struct nlmsghdr *)msg;
    nlh->nlmsg_len = NLMSG_LENGTH(GENL_HDRLEN);
    nlh->nlmsg_type = family;
    nlh->nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK;
    struct genlmsghdr *g = (struct genlmsghdr *)NLMSG_DATA(nlh);
    g->cmd = NL80211_CMD_SET_POWER_SAVE;
    g->version = 1;
    unsigned int idx = if_nametoindex(ifname);
    if (!idx) { fprintf(stderr, "no interface %s\n", ifname); return -1; }
    NLA_PUT(nlh, NL80211_ATTR_IFINDEX, sizeof(idx), &idx);
    NLA_PUT(nlh, NL80211_ATTR_PS_STATE, sizeof(state), &state);
    return nl_talk(fd, nlh);
}

static int reg_set(int fd, int family, const char *alpha2)
{
    char msg[512] = {0};
    struct nlmsghdr *nlh = (struct nlmsghdr *)msg;
    nlh->nlmsg_len = NLMSG_LENGTH(GENL_HDRLEN);
    nlh->nlmsg_type = family;
    nlh->nlmsg_flags = NLM_F_REQUEST | NLM_F_ACK;
    struct genlmsghdr *g = (struct genlmsghdr *)NLMSG_DATA(nlh);
    g->cmd = NL80211_CMD_REQ_SET_REG;
    g->version = 1;
    if (strlen(alpha2) != 2) { fprintf(stderr, "alpha2 must be 2 chars\n"); return -1; }
    NLA_PUT(nlh, NL80211_ATTR_REG_ALPHA2, 2, alpha2);
    return nl_talk(fd, nlh);
}

int main(int argc, char **argv)
{
    if (argc < 2) { fprintf(stderr, "usage: %s ps-off|ps-on|ps-get|reg <XX> [iface]\n", argv[0]); return 2; }
    int fd = nl_open();
    if (fd < 0) return 1;
    int fam = resolve_nl80211(fd);
    if (fam < 0) { fprintf(stderr, "nl80211 family not found\n"); return 1; }
    const char *iface = (argc > 3) ? argv[3] : "wlan0";

    if (!strcmp(argv[1], "ps-off")) {
        if (ps_set(fd, fam, iface, 0)) return 1;
        printf("power save OFF on %s\n", iface);
    } else if (!strcmp(argv[1], "ps-on")) {
        if (ps_set(fd, fam, iface, 1)) return 1;
        printf("power save ON on %s\n", iface);
    } else if (!strcmp(argv[1], "reg") && argc >= 3) {
        if (reg_set(fd, fam, argv[2])) return 1;
        printf("regdomain set to %s\n", argv[2]);
    } else {
        fprintf(stderr, "unknown command\n");
        return 2;
    }
    return 0;
}
