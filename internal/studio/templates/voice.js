/* Speaker playback is separate from model/tool progress. Only user input or
   begin_conversation opens the speaker; dictation never uses this controller. */
(function(){
  window.VSTDVoice={
    create(audio,callbacks){
      let talking=false,blocked=false,closed=false,attempt=0,input='',lastInput=0;
      audio.autoplay=true;audio.muted=true;
      function state(){callbacks.state('listening',talking?'Vessica in conversation · listening':'Vessica listening · say "Vessica" or Shift+V to talk');}
      function play(){
        if(closed||!audio.srcObject)return;
        const token=++attempt;
        try{
          Promise.resolve(audio.play()).then(()=>{
            if(closed||token!==attempt)return;
            const recovered=blocked;blocked=false;
            if(recovered&&talking)state();
          }).catch(()=>failed(token));
        }catch(_){failed(token);}
      }
      function failed(token){
        if(closed||token!==attempt||!talking)return;
        blocked=true;callbacks.state('error','Vessica audio blocked · click here to enable sound');
      }
      function begin(){
        if(closed||talking)return;
        input='';talking=true;audio.muted=false;callbacks.conversation(true);
        state();play();
      }
      function end(){
        input='';attempt++;talking=false;blocked=false;audio.muted=true;
        callbacks.conversation(false);if(!closed)state();
      }
      return {
        attach(stream){if(closed)return;audio.srcObject=stream;play();},
        begin,end,
        talking:()=>talking,
        blocked:()=>blocked,
        retry(){if(!closed&&talking){blocked=false;state();play();}},
        input(delta){
          if(closed||typeof delta!=='string')return;
          const now=Date.now();if(now-lastInput>2000)input='';lastInput=now;
          input=(input+delta).slice(-200);
          const text=input.replace(/[\u2018\u2019]/g,"'");
          const wake=/\bvessica\b/i.test(text);
          const stop=/\b(?:that's all|that is all|be quiet|stop speaking|stop talking|thank you|thanks)\b/i.test(text);
          if(stop&&(talking||wake)){end();return;}
          if(wake&&!talking)begin();
        },
        close(){closed=true;end();audio.pause();audio.srcObject=null;}
      };
    }
  };
})();
