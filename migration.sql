create table friend_group
(
    id         int auto_increment
        primary key,
    uid        varchar(40) default ''                not null comment '用户id',
    name       varchar(50)                           not null comment '分组名称',
    created_at timestamp   default CURRENT_TIMESTAMP not null comment '创建时间',
    updated_at timestamp   default CURRENT_TIMESTAMP not null on update CURRENT_TIMESTAMP comment '更新时间',
    version    bigint      default 1                 not null,
    is_deleted smallint    default 0                 not null,
    is_default smallint    default 0                 not null,
    constraint idx_uid_name
        unique (uid, name)
)
    comment '好友分组表';




create table friend_group_member
(
    id         int auto_increment
        primary key,
    group_id   int                                 not null comment '好友分组ID',
    friend_uid varchar(40)                         not null comment '好友用户ID',
    uid        varchar(40)                         not null comment '所属用户ID（冗余设计）',
    created_at timestamp default CURRENT_TIMESTAMP not null comment '创建时间',
    updated_at timestamp default CURRENT_TIMESTAMP not null on update CURRENT_TIMESTAMP comment '更新时间',
    version    bigint    default 1                 not null,
    is_deleted smallint  default 0                 not null,
    constraint idx_friend_user
        unique (uid, friend_uid),
    constraint idx_group_friend
        unique (group_id, friend_uid)
)
    comment '好友分组成员表';

create index idx_user_group
    on tsdd.friend_group_member (uid, group_id);




create table friend
(
    id              int auto_increment
        primary key,
    uid             varchar(40)  default ''                not null comment '用户UID',
    to_uid          varchar(40)  default ''                not null comment '好友uid',
    remark          varchar(100) default ''                not null comment '对好友的备注 TODO: 此字段不再使用，已经迁移到user_setting表',
    flag            smallint     default 0                 not null comment '好友标示',
    version         bigint       default 0                 not null comment '版本号',
    vercode         varchar(100) default ''                not null comment '验证码 加好友来源',
    source_vercode  varchar(100) default ''                not null comment '好友来源',
    is_deleted      smallint     default 0                 not null comment '是否已删除',
    is_alone        smallint     default 0                 not null comment '单项好友',
    initiator       smallint     default 0                 not null comment '加好友发起方',
    created_at      timestamp    default CURRENT_TIMESTAMP not null comment '创建时间',
    updated_at      timestamp    default CURRENT_TIMESTAMP not null comment '更新时间',
    friend_group_id bigint       default 0                 not null,
    constraint to_uid_uid
        unique (uid, to_uid)
);


create table user
(
    id                   int auto_increment
        primary key,
    uid                  varchar(40)    default ''                not null,
    name                 varchar(100)   default ''                not null,
    short_no             varchar(40)    default ''                not null,
    short_status         smallint       default 0                 not null,
    sex                  smallint       default 0                 not null,
    robot                smallint       default 0                 not null,
    category             varchar(40)    default ''                not null,
    role                 varchar(40)    default ''                not null,
    username             varchar(100)   default ''                not null,
    password             varchar(40)    default ''                not null,
    zone                 varchar(20)                              null,
    phone                varchar(100)                             null,
    chat_pwd             varchar(40)    default ''                not null,
    lock_screen_pwd      varchar(40)    default ''                not null,
    lock_after_minute    int            default 0                 not null,
    vercode              varchar(100)   default ''                not null,
    is_upload_avatar     smallint       default 0                 not null,
    qr_vercode           varchar(100)   default ''                not null,
    device_lock          smallint       default 0                 not null,
    search_by_phone      smallint       default 1                 not null,
    search_by_short      smallint       default 1                 not null,
    new_msg_notice       smallint       default 1                 not null,
    msg_show_detail      smallint       default 1                 not null,
    voice_on             smallint       default 1                 not null,
    shock_on             smallint       default 1                 not null,
    mute_of_app          smallint       default 0                 not null,
    offline_protection   smallint       default 0                 not null,
    version              bigint         default 0                 not null,
    status               smallint       default 1                 not null,
    bench_no             varchar(40)    default ''                not null,
    created_at           timestamp      default CURRENT_TIMESTAMP not null,
    updated_at           timestamp      default CURRENT_TIMESTAMP not null,
    app_id               varchar(40)    default ''                not null comment 'app id',
    email                varchar(100)   default ''                not null comment 'email地址',
    is_destroy           smallint       default 0                 not null comment '是否已销毁',
    wx_openid            varchar(100)   default ''                not null comment '微信openid',
    wx_unionid           varchar(100)   default ''                not null comment '微信unionid',
    gitee_uid            varchar(100)   default ''                not null comment 'gitee的用户id',
    github_uid           varchar(100)   default ''                not null comment 'github的用户id',
    web3_public_key      varchar(200)   default ''                not null comment 'web3公钥',
    msg_expire_second    bigint         default 0                 not null comment '消息过期时长(单位秒)',
    role_id              int            default 0                 not null comment '用户角色',
    reg_ip               varchar(40)    default ''                not null,
    reg_device_id        varchar(40)    default ''                not null comment '注册设备id',
    reg_device_type      varchar(20)    default ''                not null comment '注册设备类型',
    reg_country          varchar(20)    default ''                not null comment '注册国家',
    reg_province         varchar(20)    default ''                not null comment '注册省',
    reg_city             varchar(20)    default ''                not null comment '注册城市',
    login_ip             varchar(40)    default ''                not null,
    login_device_id      varchar(40)    default ''                not null comment '登录设备id',
    login_device_type    varchar(20)    default ''                not null comment '登录设备类型',
    login_country        varchar(20)    default ''                not null comment '登录国家',
    login_province       varchar(20)    default ''                not null comment '登录省',
    login_city           varchar(20)    default ''                not null comment '登录城市',
    ip_white_list        varchar(128)   default ''                not null comment 'ip白名单',
    reg_device           varchar(20)    default ''                not null comment '注册设备',
    reg_source           varchar(20)    default ''                not null comment '注册来源',
    totp_secret          varchar(32)    default ''                not null comment 'TOTP 密钥',
    totp_enable          tinyint        default 0                 not null comment '是否启用，1:启用，0：禁用',
    avatar               varchar(255)   default ''                not null comment '头像地址',
    balance              decimal(15, 2) default 0.00              not null comment '余额',
    wechat_payment_code  varchar(255)   default ''                not null comment '微信收款码',
    alipay_payment_code  varchar(255)   default ''                not null comment '支付宝收款码',
    frozen_balance       decimal(15, 2) default 0.00              not null comment '冻结余额',
    balance_version      bigint         default 0                 not null comment '余额版本号',
    usdt_payment_code    varchar(255)   default ''                not null comment 'USDT收款码',
    usdt_payment_address varchar(255)   default ''                not null comment 'USDT地址',
    teen_mode_pwd        varchar(255)   default ''                not null comment '青少年模式密码',
    constraint short_no_udx
        unique (short_no),
    constraint uid
        unique (uid)
);



现在有个需求：
1. 遍历user表，为每个user生成一个 默认friend_group,即设置friend_group表中的 is_default字段为1，name字段为"我的好友"，uid字段为对应的user的uid，并插入到friend_group表中。
2. 遍历friend_group和friend_group_member表，将属于同一个用户的好友按group_id进行分组，过滤掉is_deleted=1的记录
3. 然后跟心friend表，根据第二步的结果更新friend表中的friend_group_id字段，设置为对应的group_id值,如果没有对应的group_id，则设置为默认分组的id。


