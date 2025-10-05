



export * from "./model";
export * from "./const";
export * from "./conversation_manager";
export * from "./connect_manager";
// export * from "./index"
export * from "./proto"
export * from "./chat_manager"
export * from "./task"
export * from "./channel_manager"
export * from "./provider"
export * from "./event_manager"
export * from "./config"

export {default as WKSDK} from "./wksdk"

// const self = WKSDK.shared();
// window['wksdk'] = self;  /* tslint:disable-line */ // 这样普通的JS就可以通过window.wksdk获取到app对象
// export default self;